package main

import (
	"flag"
	"fmt"

	"loom/internal/egress"
)

// runEgress implements `loom egress check`: validates a policy file the
// way serve would load it (mode required, every entry validated) so an
// operator can lint the posture before deploying it. With an explicit
// path the file must exist; with no path it checks the default
// <data>/egress-policy.json and reports the allow-all default when the
// file is absent.
func runEgress(args []string) error {
	if len(args) == 0 || args[0] != "check" {
		return fmt.Errorf("usage: loom egress check [policy.json] [-data <dir>]")
	}
	fs := flag.NewFlagSet("egress check", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory (for the default policy path)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) > 1 {
		return fmt.Errorf("usage: loom egress check [policy.json] [-data <dir>]")
	}
	path := ""
	if len(rest) == 1 {
		path = rest[0]
	}
	policy, err := egress.LoadPolicyFile(path, *data)
	if err != nil {
		return err
	}
	if policy.Mode == egress.ModeDeny {
		fmt.Printf("egress policy: deny (%d allowlisted destinations)\n", len(policy.Allowlist))
		for _, entry := range policy.Allowlist {
			fmt.Printf("  %s\n", entry)
		}
		return nil
	}
	fmt.Println("egress policy: allow (all destinations permitted)")
	return nil
}
