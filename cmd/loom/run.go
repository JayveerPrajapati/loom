package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"loom/internal/cassette"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"os"
	"strings"
)

func runFlow(args []string, test bool) error {
	name := "run"
	if test {
		name = "test"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	live := fs.Bool("live", false, "use the real network")
	auto := fs.Bool("auto-approve", false, "auto-approve approval nodes")
	inputRaw := fs.String("input", "{}", "trigger input as JSON")
	verbose := fs.Bool("verbose", false, "print every event")
	subprocess := fs.Bool("enable-subprocess-nodes", os.Getenv("LOOM_ENABLE_SUBPROCESS_NODES") == "1", "allow code.python subprocess execution (deliberate security opt-in)")
	recordEffects := fs.Bool("record-effects", false, "record external effects so runs replay hermetically without network access")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: loom %s [options] <file>", name)
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

	if !test {
		if plan, err := graph.BuildPlan(wf); err == nil {
			fmt.Printf("plan: %s (%s) — %d parallel levels\n", wf.Name, wf.ID, len(plan.Levels))
			for i, level := range plan.Levels {
				fmt.Printf("  level %d: %s\n", i+1, strings.Join(level, ", "))
			}
		}
	}

	input := flow.Item{}
	if strings.TrimSpace(*inputRaw) != "" {
		if err := json.Unmarshal([]byte(*inputRaw), &input); err != nil {
			return fmt.Errorf("input: %w", err)
		}
	}

	st, eng, err := openRuntimeOpts(runtimeOptions{data: *data, live: *live, subprocess: *subprocess})
	if err != nil {
		return err
	}
	if *recordEffects {
		eng.DefaultEffects = cassette.ModeRecord
	}
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	res, err := eng.Start(ctx, wf, engine.Options{
		Trigger:     "manual",
		Input:       input,
		AutoApprove: *auto || test,
	})
	if err != nil {
		return err
	}

	printRun(res, *verbose)
	if res.Status == "failed" || res.Status == "cancelled" {
		return errRunFailed
	}
	if test && res.Status != "done" {
		return errRunFailed
	}
	return nil
}

func runReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	live := fs.Bool("live", false, "use the real network for re-execution")
	step := fs.String("step", "", "verify this node against its recorded blobs")
	execute := fs.Bool("execute", false, "with --step, re-run the node live (may repeat side effects)")
	hermetic := fs.Bool("hermetic", false, "replay from recorded effects with no network access")
	from := fs.String("from", "", "re-execute from this node onward")
	subprocess := fs.Bool("enable-subprocess-nodes", os.Getenv("LOOM_ENABLE_SUBPROCESS_NODES") == "1", "allow code.python subprocess execution on live re-runs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom replay [options] <runID>")
	}
	if *step != "" && *from != "" {
		return errors.New("--step and --from are mutually exclusive")
	}
	if *execute && *step == "" {
		return errors.New("--execute requires --step")
	}
	if *hermetic && *step == "" && *from == "" {
		return errors.New("--hermetic requires --step or --from")
	}
	if *hermetic && *execute {
		return errors.New("--hermetic and --execute are mutually exclusive")
	}

	mode := engine.ReplayFull
	node := ""
	switch {
	case *step != "":
		switch {
		case *hermetic:
			mode, node = engine.ReplayStepHermetic, *step
		case *execute:
			mode, node = engine.ReplayStepExecute, *step
		default:
			mode, node = engine.ReplayStep, *step
		}
	case *from != "":
		if *hermetic {
			mode, node = engine.ReplayFromHermetic, *from
		} else {
			mode, node = engine.ReplayFrom, *from
		}
	}

	st, eng, err := openRuntimeOpts(runtimeOptions{data: *data, live: *live, subprocess: *subprocess})
	if err != nil {
		return err
	}
	defer st.Close()

	// Say what is about to happen before it happens: live re-execution
	// repeats external effects, and users must see that before the first
	// request goes out, not in the report afterwards.
	switch mode {
	case engine.ReplayStepExecute:
		if *live {
			fmt.Fprintln(os.Stderr, "re-running live: this node re-executes for real; external effects repeat; the recorded run is not modified")
		} else {
			fmt.Fprintln(os.Stderr, "re-running against the simulated host; no external effects; the recorded run is not modified")
		}
	case engine.ReplayFrom:
		if *live {
			fmt.Fprintln(os.Stderr, "replaying live from this node onward: nodes re-execute for real; external effects repeat; the recorded run is not modified")
		} else {
			fmt.Fprintln(os.Stderr, "replaying from this node onward against the simulated host; no external effects; the recorded run is not modified")
		}
	case engine.ReplayFromHermetic, engine.ReplayStepHermetic:
		fmt.Fprintln(os.Stderr, "replaying from the recorded effects; no network access; the recorded run is not modified")
	}

	res, err := eng.Replay(context.Background(), fs.Arg(0), node, mode)
	if err != nil {
		return err
	}

	switch mode {
	case engine.ReplayStep, engine.ReplayStepExecute, engine.ReplayStepHermetic:
		fmt.Printf("step %s: match=%v live=%v hermetic=%v effects=%d\n  recorded: %s\n  actual:   %s\n",
			node, res.Match, mode == engine.ReplayStepExecute, mode == engine.ReplayStepHermetic,
			res.Effects, res.RecordedHash, res.ActualHash)
		reportDrift(res)
	case engine.ReplayFrom, engine.ReplayFromHermetic:
		fmt.Printf("replay from %s: %s hermetic=%v (%d events)\n", node, res.Status,
			mode == engine.ReplayFromHermetic, len(res.Events))
		reportDrift(res)
		if res.Status == "failed" || res.Status == "cancelled" {
			return errRunFailed
		}
	case engine.ReplayFull:
		fmt.Printf("replay %s: status=%s verified=%v (%d events)\n", res.RunID, res.Status, res.Match, len(res.Events))
	}

	if mode != engine.ReplayFrom && mode != engine.ReplayFromHermetic && !res.Match {
		return errRunFailed
	}
	if len(res.Diffs) > 0 || len(res.Misses) > 0 {
		return errRunFailed
	}
	return nil
}

func reportDrift(res *engine.ReplayResult) {
	for _, miss := range res.Misses {
		fmt.Printf("  cassette miss: %s effect #%d\n", miss.Kind, miss.Index)
	}
	for _, diff := range res.Diffs {
		fmt.Printf("  request drift: %s effect #%d recorded=%s actual=%s\n",
			diff.Kind, diff.Index, shortHash(diff.RecordedHash), shortHash(diff.ActualHash))
	}
}

func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	runID := fs.String("from-run", "", "historical run to verify against (required)")
	version := fs.Int("version", 0, "workflow version to verify (0 = the snapshot recorded on the run)")
	live := fs.Bool("live", false, "resolve credentials from the store (verification never calls the network)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runID == "" || fs.NArg() != 0 {
		return errors.New("usage: loom verify --from-run <runID> [--version N]")
	}
	st, eng, err := openRuntimeOpts(runtimeOptions{data: *data, live: *live})
	if err != nil {
		return err
	}
	defer st.Close()

	candidate, err := eng.CandidateWorkflow(*runID, *version)
	if err != nil {
		return err
	}
	verdict, err := eng.VerifyAgainstRun(context.Background(), *runID, candidate)
	if err != nil {
		return err
	}
	fmt.Println(verdict.Summary())
	for _, node := range verdict.Nodes {
		label := node.Name
		if label == "" {
			label = node.NodeID
		}
		switch node.Status {
		case "match":
			fmt.Printf("  ok    %s\n", label)
		case "drift":
			fmt.Printf("  DRIFT %s recorded=%s actual=%s\n", label, shortHash(node.RecordedHash), shortHash(node.ActualHash))
		case "blocked":
			fmt.Printf("  BLOCK %s (missing recorded effect or failed node)\n", label)
		case "added":
			fmt.Printf("  NEW   %s (no recorded baseline)\n", label)
		case "removed":
			fmt.Printf("  GONE  %s (no longer in the candidate graph)\n", label)
		}
	}
	if !verdict.Passed {
		return errRunFailed
	}
	return nil
}

func runResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	live := fs.Bool("live", false, "use the real network")
	decision := fs.String("decision", "approved", "approved or rejected")
	note := fs.String("note", "", "decision note (required when rejecting)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom resume [options] <runID>")
	}
	if *decision != "approved" && *decision != "rejected" {
		return errors.New("--decision must be approved or rejected")
	}
	if *decision == "rejected" && strings.TrimSpace(*note) == "" {
		return errors.New("--note is required when --decision rejected")
	}

	st, eng, err := openRuntimeOpts(runtimeOptions{data: *data, live: *live})
	if err != nil {
		return err
	}
	defer st.Close()

	res, err := eng.ResumeDecision(context.Background(), fs.Arg(0), *decision,
		engine.DecisionMeta{ActorID: "cli", ActorLabel: "cli", Source: "cli"}, *note)
	if err != nil {
		return err
	}
	printRun(res, false)
	if res.Status == "failed" || res.Status == "cancelled" {
		return errRunFailed
	}
	return nil
}

func printRun(res *engine.Result, verbose bool) {
	fmt.Printf("run %s: %s", res.RunID, res.Status)
	if res.ResumeNode != "" {
		fmt.Printf(" at %s", res.ResumeNode)
	}
	if len(res.Events) > 0 {
		fmt.Printf(" (%d events)", len(res.Events))
	}
	fmt.Println()

	if res.ResumeNode != "" {
		fmt.Printf("  resume with: loom resume %s --decision approved\n", res.RunID)
	}
	if res.Error != "" {
		fmt.Printf("  error: %s\n", res.Error)
	}
	if verbose {
		for _, ev := range res.Events {
			node := ev.NodeID
			if node == "" {
				node = "-"
			}
			fmt.Printf("  %6d %-14s %-18s %5dms %s\n", ev.Seq, ev.Type, node, ev.DurationMs, ev.Error)
		}
	}
}
