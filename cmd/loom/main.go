package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/runbook"
	"loom/internal/secrets"
	"loom/internal/server"
	"loom/internal/store"
)

var version = "0.1.0-dev"

var (
	errValidation = errors.New("validation failed")
	errRunFailed  = errors.New("run did not complete")
)

type jsonLogWriter struct{}

func (jsonLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	record := map[string]any{
		"time":  time.Now().UTC().Format(time.RFC3339Nano),
		"level": "info",
		"msg":   msg,
	}
	data, _ := json.Marshal(record)
	data = append(data, '\n')
	return os.Stderr.Write(data)
}

func main() {
	if os.Getenv("LOOM_LOG_FORMAT") == "json" {
		log.SetFlags(0)
		log.SetOutput(jsonLogWriter{})
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "validate":
		err = runValidate(os.Args[2:])
	case "plan":
		err = runPlan(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "run":
		err = runFlow(os.Args[2:], false)
	case "test":
		err = runFlow(os.Args[2:], true)
	case "replay":
		err = runReplay(os.Args[2:])
	case "resume":
		err = runResume(os.Args[2:])
	case "export":
		err = runExport(os.Args[2:])
	case "import":
		err = runImport(os.Args[2:])
	case "workflows":
		err = runWorkflows(os.Args[2:])
	case "runs":
		err = runRuns(os.Args[2:])
	case "runbook":
		err = runRunbook(os.Args[2:])
	case "prune":
		err = runPrune(os.Args[2:])
	case "version":
		fmt.Println("loom " + version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "loom: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err == nil {
		return
	}
	if errors.Is(err, errValidation) || errors.Is(err, errRunFailed) {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "loom:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `loom — typed, replayable workflows

usage:
  loom validate <file...>              validate one or more workflow files
  loom plan <file>                     print the execution DAG in parallel levels
  loom run [options] <file>            run a workflow (simulated by default)
  loom test [options] <file>           run with approvals auto-approved
  loom replay [options] <runID>        replay a recorded run
  loom resume [options] <runID>        resume a run waiting for approval
  loom export [options] <workflowID>   export a workflow as JSON
  loom import [options] <file>         import a workflow into the store
  loom workflows [options]             list stored workflows
  loom runs [options]                  list runs
  loom runbook [options] <file|id>     render a Markdown runbook
  loom prune [options]                 delete old runs and orphan blobs
  loom serve [options]                 start the API and embedded editor
  loom version                         print the version

run/test options:
  --data DIR        data directory (default ./data)
  --live            use the real network (default: simulated)
  --auto-approve    auto-approve approval nodes
  --input JSON      trigger input object (default {})
  --verbose         print every event

replay options:
  --data DIR        data directory (default ./data)
  --step NODE       re-execute one node and compare hashes
  --from NODE       re-execute from a node onward (live host unless --live is off)
  --live            use the real network for re-execution

prune options:
  --data DIR        data directory (default ./data)
  --before VALUE    cutoff: 30d, 24h or RFC3339 timestamp (default 30d)

serve options:
  --addr ADDR       listen address (default :5678)
  --data DIR        data directory (default ./data)
  --token TOKEN     require Authorization: Bearer TOKEN (default $LOOM_TOKEN)
  --retention-days N  prune terminal runs older than N days on boot and daily (0 = off)
  --sim             use the deterministic simulated host (tests and demos)

docs: docs/v1-scope.md · plan: PLAN.md
`)
}

func runValidate(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom validate <file...>")
	}

	args = expandGlobs(args)
	failed := false
	for _, path := range args {
		wf, err := graph.Load(path)
		if err != nil {
			fmt.Printf("%s: error: %v\n", path, err)
			failed = true
			continue
		}
		report := graph.Validate(wf)
		if !report.HasIssues() {
			fmt.Printf("%s: ok (%d nodes, %d edges)\n", path, len(wf.Nodes), len(wf.Edges))
			continue
		}
		for _, issue := range report.Issues {
			fmt.Printf("%s: %s\n", path, issue.String())
		}
		if report.HasErrors() {
			failed = true
		}
	}

	if failed {
		return errValidation
	}
	return nil
}

func expandGlobs(args []string) []string {
	var out []string
	for _, arg := range args {
		if strings.ContainsAny(arg, "*?[") {
			matches, err := filepath.Glob(arg)
			if err == nil && len(matches) > 0 {
				out = append(out, matches...)
				continue
			}
		}
		out = append(out, arg)
	}
	return out
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom plan <file>")
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

	plan, err := graph.BuildPlan(wf)
	if err != nil {
		return err
	}

	fmt.Printf("plan: %s (%s)\n", wf.Name, wf.ID)
	for i, level := range plan.Levels {
		fmt.Printf("  %d: %s\n", i+1, strings.Join(level, ", "))
	}
	return nil
}

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

	st, eng, err := openRuntime(*data, *live)
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
	step := fs.String("step", "", "re-execute this node and compare hashes")
	from := fs.String("from", "", "re-execute from this node onward")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom replay [options] <runID>")
	}
	if *step != "" && *from != "" {
		return errors.New("--step and --from are mutually exclusive")
	}

	mode := engine.ReplayFull
	node := ""
	switch {
	case *step != "":
		mode, node = engine.ReplayStep, *step
	case *from != "":
		mode, node = engine.ReplayFrom, *from
	}

	st, eng, err := openRuntime(*data, *live)
	if err != nil {
		return err
	}
	defer st.Close()

	res, err := eng.Replay(context.Background(), fs.Arg(0), node, mode)
	if err != nil {
		return err
	}

	switch mode {
	case engine.ReplayStep:
		fmt.Printf("step %s: match=%v\n  recorded: %s\n  actual:   %s\n", node, res.Match, res.RecordedHash, res.ActualHash)
	case engine.ReplayFrom:
		fmt.Printf("replay from %s: %s (%d events)\n", node, res.Status, len(res.Events))
		if res.Status == "failed" || res.Status == "cancelled" {
			return errRunFailed
		}
	case engine.ReplayFull:
		fmt.Printf("replay %s: status=%s verified=%v (%d events)\n", res.RunID, res.Status, res.Match, len(res.Events))
	}

	if mode != engine.ReplayFrom && !res.Match {
		return errRunFailed
	}
	return nil
}

func runResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	live := fs.Bool("live", false, "use the real network")
	decision := fs.String("decision", "approved", "approved or rejected")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: loom resume [options] <runID>")
	}
	if *decision != "approved" && *decision != "rejected" {
		return errors.New("--decision must be approved or rejected")
	}

	st, eng, err := openRuntime(*data, *live)
	if err != nil {
		return err
	}
	defer st.Close()

	res, err := eng.Resume(context.Background(), fs.Arg(0), *decision)
	if err != nil {
		return err
	}
	printRun(res, false)
	if res.Status == "failed" || res.Status == "cancelled" {
		return errRunFailed
	}
	return nil
}

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
	result, err := st.Prune(cutoff)
	if err != nil {
		return err
	}
	fmt.Printf("pruned %d runs, %d events, %d blobs before %s\n",
		result.Runs, result.Events, result.Blobs, cutoff.UTC().Format(time.RFC3339))
	return nil
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

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":5678", "listen address")
	data := fs.String("data", "./data", "data directory")
	token := fs.String("token", os.Getenv("LOOM_TOKEN"), "bearer token required for API requests")
	prod := fs.Bool("prod", os.Getenv("LOOM_PROD") == "true" || os.Getenv("LOOM_PROD") == "1", "enforce strict enterprise production mode")
	retentionDays := fs.Int("retention-days", 0, "prune terminal runs older than N days (0 = off)")
	sim := fs.Bool("sim", false, "use the deterministic simulated host")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *prod {
		if strings.TrimSpace(*token) == "" {
			return errors.New("FATAL: --prod mode requires a non-empty bearer token (--token or LOOM_TOKEN environment variable)")
		}
		if len(strings.TrimSpace(*token)) < 16 {
			return errors.New("FATAL: --prod mode requires a strong bearer token (minimum 16 characters)")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, eng, err := openRuntime(*data, !*sim)
	if err != nil {
		return err
	}
	defer st.Close()

	hostname, _ := os.Hostname()
	owner := fmt.Sprintf("%s/%d", hostname, os.Getpid())
	const leaseTTL = 5 * time.Second

	var schedMu sync.Mutex
	var scheduler *server.Scheduler
	var cancelSched context.CancelFunc

	startScheduler := func() error {
		schedMu.Lock()
		defer schedMu.Unlock()
		if scheduler != nil {
			return nil
		}
		var sCtx context.Context
		sCtx, cancelSched = context.WithCancel(ctx)
		if stale, err := st.StaleRuns(); err != nil {
			log.Printf("stale run scan: %v", err)
		} else {
			for _, run := range stale {
				run := run
				log.Printf("resuming interrupted run %s (%s)", run.ID, run.Status)
				go func() {
					if _, err := eng.ResumeInterrupted(sCtx, run.ID); err != nil {
						log.Printf("resume interrupted run %s: %v", run.ID, err)
					}
				}()
			}
		}
		if *retentionDays > 0 {
			prune := func() {
				result, err := st.Prune(time.Now().AddDate(0, 0, -*retentionDays))
				if err != nil {
					log.Printf("retention prune: %v", err)
					return
				}
				if result.Runs > 0 || result.Blobs > 0 {
					log.Printf("retention pruned %d runs, %d events, %d blobs", result.Runs, result.Events, result.Blobs)
				}
			}
			prune()
			go func() {
				ticker := time.NewTicker(24 * time.Hour)
				defer ticker.Stop()
				for {
					select {
					case <-sCtx.Done():
						return
					case <-ticker.C:
						prune()
					}
				}
			}()
		}

		if n, err := server.MigrateCronWorkflows(st); err != nil {
			log.Printf("cron migration: %v", err)
		} else if n > 0 {
			log.Printf("migrated %d workflow cron trigger(s) into jobs", n)
		}

		scheduler = server.NewScheduler(sCtx, st, eng)
		if err := scheduler.Reload(); err != nil {
			return err
		}
		scheduler.Start()
		log.Printf("scheduler started (active instance: %s)", owner)
		return nil
	}

	stopScheduler := func() {
		schedMu.Lock()
		defer schedMu.Unlock()
		if scheduler != nil {
			scheduler.Stop()
			scheduler = nil
		}
		if cancelSched != nil {
			cancelSched()
			cancelSched = nil
		}
	}
	defer func() {
		stopScheduler()
		_ = st.ReleaseLease("scheduler", owner)
		eng.CancelAll()
	}()

	owned, err := st.AcquireLease("scheduler", owner, leaseTTL)
	if err != nil {
		return err
	}
	if owned {
		if err := startScheduler(); err != nil {
			return err
		}
	} else {
		log.Printf("scheduler lease held by another instance; standby will monitor for failover")
	}

	go func() {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		isLeader := owned
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ok, err := st.AcquireLease("scheduler", owner, leaseTTL)
				if err != nil {
					log.Printf("scheduler lease check: %v", err)
					continue
				}
				if ok && !isLeader {
					if err := startScheduler(); err == nil {
						isLeader = true
					}
				} else if !ok && isLeader {
					log.Printf("scheduler lease lost; reverting to standby")
					stopScheduler()
					isLeader = false
				}
			}
		}
	}()

	reload := func() error {
		schedMu.Lock()
		defer schedMu.Unlock()
		if scheduler != nil {
			return scheduler.Reload()
		}
		return nil
	}

	if *token != "" {
		fmt.Println("API authentication enabled (Bearer token)")
	}
	fmt.Printf("loom %s listening on http://localhost%s\n", version, *addr)
	return server.Run(ctx, *addr, server.Deps{
		Version: version,
		Store:   st,
		Engine:  eng,
		Token:   *token,
		Reload:  reload,
	})
}

func openRuntime(data string, live bool) (*store.Store, *engine.Engine, error) {
	st, err := openStore(data)
	if err != nil {
		return nil, nil, err
	}

	resolveCred := func(id string) (flow.Credential, bool) {
		if cred, ok := secrets.FromEnv().Lookup(id); ok {
			return cred, true
		}
		if typ, raw, err := st.GetCredential(id); err == nil {
			var parsed map[string]any
			if json.Unmarshal(raw, &parsed) == nil {
				return flow.Credential{Type: typ, Data: parsed, Raw: string(raw)}, true
			}
			return flow.Credential{Type: typ, Raw: string(raw)}, true
		}
		return flow.Credential{}, false
	}

	var h flow.Host = host.NewSim()
	if live {
		h = host.NewLive(resolveCred)
	}
	eng := engine.New(st, h)
	eng.CredentialResolver = resolveCred
	return st, eng, nil
}

func openStore(data string) (*store.Store, error) {
	st, err := store.Open(data)
	if err != nil {
		return nil, err
	}
	return st, nil
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
