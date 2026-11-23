package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"loom/internal/egress"
	"loom/internal/pyenv"
	"loom/internal/store"
)

// doctorCheckpointFile is where `loom doctor` persists resumable scan
// progress. It records the last run id of the longest all-verified prefix;
// a later `--resume` continues after it instead of rescanning the store.
func doctorCheckpointFile(dataDir string) string {
	return filepath.Join(dataDir, "doctor.checkpoint")
}

// runDoctor implements `loom doctor`: a read-only integrity scan over every
// recorded run and every indexed blob. It never executes workflow code.
func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	data := fs.String("data", "data", "data directory")
	resume := fs.Bool("resume", false, "continue after the last run verified by an interrupted scan")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*data); err != nil {
		return fmt.Errorf("--data: %w", err)
	}
	s, err := openStore(*data)
	if err != nil {
		return err
	}
	defer s.Close()

	opts := store.HealthOptions{}
	checkpoint := doctorCheckpointFile(*data)
	if *resume {
		if raw, err := os.ReadFile(checkpoint); err == nil {
			opts.AfterRunID = strings.TrimSpace(string(raw))
			fmt.Printf("resuming after run %s\n", opts.AfterRunID)
		} else {
			fmt.Println("no checkpoint found; scanning from the beginning")
		}
	}
	var lastPersist time.Time
	opts.OnProgress = func(runID string, runs, events int) {
		now := time.Now()
		if now.Sub(lastPersist) < 2*time.Second {
			return
		}
		lastPersist = now
		if err := os.WriteFile(checkpoint, []byte(runID), 0o600); err == nil {
			fmt.Printf("\rscanned %d runs (%d events)", runs, events)
		}
	}
	report, err := s.CheckHealthOpts(opts)
	if err != nil {
		return err
	}
	fmt.Printf("data directory: %s\n", *data)
	fmt.Printf("  runs: %d (%d events), blobs indexed: %d, keys: %d\n",
		report.RunsChecked, report.EventsChecked, report.BlobsIndexed, report.KeyCount)
	if report.PinnedSecret {
		fmt.Println("  signing key: pinned by environment (rotation unavailable)")
	} else {
		fmt.Printf("  signing key: current %s, legacy %s\n", report.CurrentKeyID, report.LegacyKeyID)
	}
	for _, fail := range report.ChainFailures {
		fmt.Printf("  CHAIN FAILURE  %s\n", fail)
	}
	for _, fail := range report.BlobFailures {
		fmt.Printf("  BLOB FAILURE   %s\n", fail)
	}
	for _, miss := range report.ReferencedMissing {
		fmt.Printf("  MISSING BLOB   %s\n", miss)
	}
	for _, orph := range report.OrphanBlobFiles {
		fmt.Printf("  ORPHAN FILE    %s\n", orph)
	}
	for _, fail := range report.OwnershipFailures {
		fmt.Printf("  OWNERSHIP      %s\n", fail)
	}
	if report.BootstrapPasswordSet {
		fmt.Println("  WARNING      LOOM_BOOTSTRAP_ADMIN_PASSWORD is still set; it has no effect")
		fmt.Println("               once users exist — unset it to avoid leaking the initial password")
	}
	if !report.OK() {
		// Keep the checkpoint: it marks the verified prefix, and `--resume`
		// must re-check the failing runs rather than skip past them.
		return fmt.Errorf("doctor: %d chain, %d blob, %d referenced-missing, %d orphan, %d ownership findings (rerun with --resume to continue after the verified prefix)",
			report.ChainFailureCount, report.BlobFailureCount, report.ReferencedMissingCount, report.OrphanBlobFileCount, report.OwnershipFailureCount)
	}
	if err := os.Remove(checkpoint); err == nil {
		fmt.Println("  checkpoint cleared")
	}
	fmt.Println("  all event chains verified; all blobs hash to their names")

	// Python dependency environments: each directory must carry a manifest
	// whose pinned requirements hash to the directory name — the same
	// tamper-evidence model as every other artifact.
	envsRoot := filepath.Join(*data, "envs")
	policy, policyErr := pyenv.LoadPolicy(filepath.Join(*data, "subprocess-policy.json"), false)
	if policyErr != nil {
		fmt.Printf("  POLICY NOTE  subprocess policy: %v\n", policyErr)
	}
	if entries, err := os.ReadDir(envsRoot); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			manifest, err := pyenv.LoadManifest(envsRoot, entry.Name())
			if err != nil {
				fmt.Printf("  ENV FAILURE   %s: %v\n", entry.Name(), err)
				return fmt.Errorf("doctor: python environment %s failed verification: %w", entry.Name(), err)
			}
			fmt.Printf("  python env    %s verified\n", entry.Name()[:12])
			size, sizeErr := pyenv.EnvSize(envsRoot, entry.Name())
			if sizeErr == nil {
				fmt.Printf("                %d MiB on disk\n", size/(1024*1024))
			}
			// A built environment that no longer passes the policy is a
			// finding, not a failure: the operator may have tightened the
			// policy after environments were built.
			if policyErr == nil {
				if err := policy.Check(manifest.Requirements); err != nil {
					fmt.Printf("  POLICY NOTE   %s: %v\n", entry.Name()[:12], err)
				}
				if err := policy.CheckSize(envsRoot, entry.Name()); err != nil {
					fmt.Printf("  POLICY NOTE   %s: %v\n", entry.Name()[:12], err)
				}
			}
		}
	}
	if policyErr == nil {
		fmt.Printf("  subprocess policy: %s (cap %d MiB per environment)\n", strings.Join(policy.AllowedPackages, ", "), policy.MaxEnvMB)
	}
	// Egress policy: validate the default file so a bad allowlist is a
	// doctor finding before it becomes a runtime denial surprise.
	egressPol, egressErr := egress.LoadPolicyFile("", *data)
	if egressErr != nil {
		fmt.Printf("  POLICY NOTE   egress policy: %v\n", egressErr)
	} else if egressPol.Mode == egress.ModeDeny {
		fmt.Printf("  egress policy: deny (allowlist %d entries)\n", len(egressPol.Allowlist))
	} else {
		fmt.Println("  egress policy: allow (all destinations permitted)")
	}
	fmt.Println("store healthy")
	return nil
}
