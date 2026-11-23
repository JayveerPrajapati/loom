package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"loom/internal/store"
)

// runOwnership implements the explicit, reversible profile-ownership
// migration. Opening a data directory never migrates drafts: the operator
// runs a dry-run, reviews the report, creates a backup, applies, and can roll
// back. This is the command the audit's Milestone 1D requires.
func runOwnership(args []string) error {
	if len(args) == 0 || args[0] != "migrate" {
		return errors.New("usage: loom ownership migrate [--dry-run|--apply|--rollback] [options]")
	}
	return runOwnershipMigrate(args[1:])
}

func runOwnershipMigrate(args []string) error {
	fs := flag.NewFlagSet("ownership migrate", flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	dryRun := fs.Bool("dry-run", false, "write the migration report without changing drafts (default)")
	apply := fs.Bool("apply", false, "stamp drafts with explicit profile ownership")
	backup := fs.String("backup", "", "verified loom backup archive created within 24h (required with --apply)")
	rollback := fs.Bool("rollback", false, "restore the latest applied migration")
	reportPath := fs.String("report", "", "report path (default <data>/"+store.OwnershipMigrationReportFile+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *apply && *dryRun {
		return errors.New("choose --dry-run or --apply, not both")
	}
	if *rollback && (*apply || *dryRun) {
		return errors.New("--rollback cannot be combined with --dry-run or --apply")
	}
	st, err := openStore(*data)
	if err != nil {
		return err
	}
	defer st.Close()

	if *rollback {
		id, err := st.LatestOwnershipMigrationID()
		if err != nil {
			return err
		}
		if id == "" {
			return errors.New("no ownership migration has been applied")
		}
		report, err := st.RollbackProfileOwnership(id)
		if err != nil {
			return err
		}
		fmt.Printf("rolled back ownership migration %s: %d workflow(s), %d node(s)\n",
			report.MigrationID, report.Workflows, report.Nodes)
		return nil
	}

	plan, err := st.PlanProfileOwnership()
	if err != nil {
		return err
	}
	if *reportPath == "" {
		*reportPath = filepath.Join(*data, store.OwnershipMigrationReportFile)
	}
	if err := writeOwnershipReport(*reportPath, plan.Report); err != nil {
		return err
	}
	fmt.Printf("ownership migration report: %s (%d workflow(s), %d node(s): %d inherited, %d override)\n",
		*reportPath, plan.Report.Workflows, plan.Report.Nodes,
		plan.Report.Inherited, plan.Report.Overrides)
	if !plan.HasChanges() {
		fmt.Println("nothing to migrate")
		return nil
	}
	if !*apply {
		fmt.Println("dry run: no drafts changed; review the report, create a backup, then add --apply --backup <archive>")
		return nil
	}
	if *backup == "" {
		return errors.New("--apply requires --backup: run `loom backup --data <dir> --out <archive>` first")
	}
	if err := verifyOwnershipBackup(*backup); err != nil {
		return err
	}
	id := time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := st.ApplyProfileOwnership(plan, id); err != nil {
		return err
	}
	fmt.Printf("applied ownership migration %s; rollback with: loom ownership migrate --data %s --rollback\n",
		id, *data)
	return nil
}

func writeOwnershipReport(path string, report store.OwnershipMigrationReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ownership report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write ownership report: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write ownership report: %w", err)
	}
	return nil
}

// verifyOwnershipBackup is the apply gate: the archive must be recent and
// must pass the same chain, blob and ownership checks as `loom backup
// verify`. A pinned LOOM_SECRET would verify the archive under the wrong key,
// so it is refused exactly as in runBackupVerify.
func verifyOwnershipBackup(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("--backup: %w", err)
	}
	if age := time.Since(info.ModTime()); age > 24*time.Hour {
		return fmt.Errorf("--backup %s is %s old; create a fresh backup within 24h", path, age.Round(time.Minute))
	}
	if os.Getenv("LOOM_SECRET") != "" || os.Getenv("LOOM_SECRET_KEY_FILE") != "" {
		return errors.New("unset LOOM_SECRET/LOOM_SECRET_KEY_FILE to verify a backup: verification must use the keyring inside the archive")
	}
	dir, err := extractBackup(path)
	if err != nil {
		return fmt.Errorf("--backup: %w", err)
	}
	defer os.RemoveAll(dir)
	st, err := openStore(dir)
	if err != nil {
		return fmt.Errorf("--backup: open archived store: %w", err)
	}
	defer st.Close()
	report, err := st.CheckHealth()
	if err != nil {
		return fmt.Errorf("--backup: verify: %w", err)
	}
	if !report.OK() {
		failures := append([]string{}, report.ChainFailures...)
		failures = append(failures, report.BlobFailures...)
		failures = append(failures, report.ReferencedMissing...)
		failures = append(failures, report.OrphanBlobFiles...)
		failures = append(failures, report.OwnershipFailures...)
		return fmt.Errorf("--backup: archive failed verification: %s", strings.Join(failures, "; "))
	}
	fmt.Printf("backup verified: %s (%d runs, %d events)\n", path, report.RunsChecked, report.EventsChecked)
	return nil
}
