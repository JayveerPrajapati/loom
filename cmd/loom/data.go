package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"loom/internal/graph"
	"loom/internal/runbook"
	"os"
	"strconv"
	"strings"
	"time"
)

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	out := fs.String("out", "", "write to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom export [options] <workflowID>")
	}

	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()

	wf, err := st.GetWorkflow(fs.Arg(0))
	if err != nil {
		return err
	}
	// Exports are self-contained (plan §18 D8): profile references are
	// resolved inline — config, credential refs and retry policy — and the
	// dangling reference is stripped, so importing the file on an
	// instance without the profile still works.
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		return fmt.Errorf("resolve profile references: %w", err)
	}
	// Bundles and direct references alike (Phase 1 P0-2): mappings and
	// schemas resolve inline too. Like published snapshots, the asset ids
	// stay on the doc for traceability while the compiled rows and field
	// lists travel with the export — the artifact runs anywhere, and a
	// re-publish on an instance missing the asset is a named error, not
	// a mystery.
	if _, err := st.ResolveMappingRefs(wf); err != nil {
		return fmt.Errorf("resolve mapping references: %w", err)
	}
	if _, err := st.ResolveSchemaRefs(wf); err != nil {
		return fmt.Errorf("resolve schema references: %w", err)
	}
	st.ApplyCatalogDefaults(wf)
	for i := range wf.Nodes {
		// The export is self-contained: with no profile reference,
		// ownership markers are meaningless and would trip doctor on the
		// importing instance.
		wf.Nodes[i].ProfileRef = ""
		wf.Nodes[i].ProfileOverrides = nil
	}
	raw, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	if *out == "" {
		_, err = os.Stdout.Write(raw)
		return err
	}
	return os.WriteFile(*out, raw, 0o644)
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom import [options] <file>")
	}

	wf, err := graph.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	report := graph.Validate(wf)
	for _, issue := range report.Issues {
		fmt.Println(issue.String())
	}
	if report.HasErrors() {
		return errValidation
	}

	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()

	// Enforce webhook-path uniqueness here too: the API checks it, but an
	// import used to bypass the check and silently shadow an existing hook.
	if len(graph.WebhookPaths(wf)) > 0 {
		summaries, err := st.ListWorkflows()
		if err != nil {
			return err
		}
		others := make([]*graph.Workflow, 0, len(summaries))
		for _, summary := range summaries {
			if summary.ID == wf.ID {
				continue
			}
			other, err := st.GetWorkflow(summary.ID)
			if err != nil {
				continue
			}
			others = append(others, other)
		}
		if err := graph.ValidateWebhookPaths(wf, others); err != nil {
			return err
		}
	}

	if err := st.SaveWorkflow(wf); err != nil {
		return err
	}
	fmt.Printf("imported %s (%s) v%d\n", wf.ID, wf.Name, wf.Version)
	return nil
}

func runWorkflows(args []string) error {
	fs := flag.NewFlagSet("workflows", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()

	list, err := st.ListWorkflows()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no workflows")
		return nil
	}
	for _, summary := range list {
		fmt.Printf("%-24s v%-3d %-32s %s\n", summary.ID, summary.Version, summary.Name, summary.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return nil
}

func runRuns(args []string) error {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	workflow := fs.String("workflow", "", "filter by workflow id")
	limit := fs.Int("limit", 20, "maximum runs to list")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()

	runs, err := st.ListRuns(*workflow, *limit)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("no runs")
		return nil
	}
	for _, run := range runs {
		fmt.Printf("%-28s %-10s %-20s %s\n", run.ID, run.Status, run.WorkflowID, run.StartedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}

func runRunbook(args []string) error {
	fs := flag.NewFlagSet("runbook", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom runbook <file|workflowID>")
	}
	ref := fs.Arg(0)
	var wf *graph.Workflow
	var err error
	if strings.HasSuffix(ref, ".json") {
		wf, err = graph.Load(ref)
	} else {
		st, openErr := openStore(*data)
		if openErr != nil {
			return openErr
		}
		defer st.Close()
		wf, err = st.GetWorkflow(ref)
	}
	if err != nil {
		return err
	}
	fmt.Print(runbook.Render(wf))
	return nil
}

func runPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	before := fs.String("before", "30d", "cutoff (30d, 24h, or RFC3339)")
	dryRun := fs.Bool("dry-run", false, "report what would be pruned without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cutoff, err := parseCutoff(*before, time.Now())
	if err != nil {
		return err
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()
	if *dryRun {
		estimate, err := st.PruneEstimate(cutoff)
		if err != nil {
			return err
		}
		fmt.Printf("would prune %d runs, %d events, %d blobs (~%s) before %s\n",
			estimate.Runs, estimate.Events, estimate.Blobs,
			formatBytes(estimate.Bytes), cutoff.UTC().Format(time.RFC3339))
		fmt.Println("nothing was deleted; run again without --dry-run to prune")
		return nil
	}
	result, err := st.Prune(cutoff)
	if err != nil {
		return err
	}
	fmt.Printf("pruned %d runs, %d events, %d blobs (~%s reclaimed) before %s\n",
		result.Runs, result.Events, result.Blobs,
		formatBytes(result.Bytes), cutoff.UTC().Format(time.RFC3339))
	return nil
}

// formatBytes renders a storage size in the largest unit that keeps one
// significant digit.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	suffix := ""
	for _, suffix = range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}

func parseCutoff(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty retention cutoff")
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days < 0 {
			return time.Time{}, fmt.Errorf("invalid day cutoff %q", value)
		}
		return now.AddDate(0, 0, -days), nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return now.Add(-duration), nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	return time.Time{}, fmt.Errorf("invalid retention cutoff %q", value)
}
