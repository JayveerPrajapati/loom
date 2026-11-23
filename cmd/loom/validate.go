package main

import (
	"errors"
	"flag"
	"fmt"
	"loom/internal/graph"
	"path/filepath"
	"strings"
)

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
