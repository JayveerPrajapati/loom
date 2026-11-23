package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runBackup implements `loom backup`: a consistent single-file snapshot of the
// data directory as a tar.gz. The database is captured with VACUUM INTO (a
// consistent copy even under WAL), blobs and the keyring are copied verbatim.
func runBackup(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "verify":
			return runBackupVerify(args[1:])
		case "restore":
			return runBackupRestore(args[1:])
		}
	}
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	data := fs.String("data", "data", "data directory")
	out := fs.String("out", "", fmt.Sprintf("output archive (default loom-backup-<timestamp>.tar.gz)"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*data); err != nil {
		return fmt.Errorf("--data: %w", err)
	}
	if *out == "" {
		*out = "loom-backup-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s already exists", *out)
	}

	// Consistent database snapshot without the live WAL files.
	tmp, err := os.CreateTemp(".", "loom-db-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	if err := vacuumInto(*data, tmpPath); err != nil {
		return err
	}

	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	blobCount := 0
	keyCount := 0
	envCount := 0
	addFile := func(path, name string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		if _, err := io.Copy(tw, src); err != nil {
			return err
		}
		return nil
	}
	if err := addFile(tmpPath, "loom.db"); err != nil {
		return err
	}
	copyTree := func(sub string) error {
		return filepath.WalkDir(filepath.Join(*data, sub), func(path string, d os.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return werr
			}
			rel, err := filepath.Rel(*data, path)
			if err != nil {
				return err
			}
			if strings.HasPrefix(rel, "blobs/") {
				blobCount++
			}
			if strings.HasPrefix(rel, "keys/") {
				keyCount++
			}
			if strings.HasPrefix(rel, "envs/") {
				envCount++
			}
			return addFile(path, filepath.ToSlash(rel))
		})
	}
	// Python environments are content-hash addressed (data/envs/<hash>) so
	// they dedupe naturally; without them a restore cannot re-execute
	// code.python workflows without a network rebuild. The subprocess
	// package policy is security configuration: losing it silently reverts
	// to the default-allow policy.
	for _, sub := range []string{"blobs", "keys", "envs"} {
		if _, err := os.Stat(filepath.Join(*data, sub)); err == nil {
			if err := copyTree(sub); err != nil {
				return err
			}
		}
	}
	// Pre-keyring deployments keep their secret.key; include it so a restore
	// works verbatim. The chain marker matters to the same version checks.
	// head.sealed must travel with the archive: without it, restoring into a
	// fresh directory would re-stamp the head seal from whatever the archive
	// contains instead of trusting the sealed counts.
	for _, extra := range []string{"secret.key", "chain.migrated", "head.sealed", "subprocess-policy.json"} {
		path := filepath.Join(*data, extra)
		if _, err := os.Stat(path); err == nil {
			if err := addFile(path, extra); err != nil {
				return err
			}
			if extra == "secret.key" {
				keyCount++
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	fmt.Printf("backup written: %s (%d blobs, %d key files, %d env files)\n", *out, blobCount, keyCount, envCount)
	fmt.Println("verify it any time with: loom backup verify --backup " + *out)
	return nil
}

func vacuumInto(dataDir, dest string) error {
	s, err := openStore(dataDir)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.VacuumInto(dest)
}

func runBackupVerify(args []string) error {
	fs := flag.NewFlagSet("backup verify", flag.ContinueOnError)
	backup := fs.String("backup", "", "archive to verify (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *backup == "" {
		return errors.New("--backup is required")
	}
	dir, err := extractBackup(*backup)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// A pinned env secret would verify under a different key than the one the
	// archive carries; that is a user error, not a corrupt backup.
	if os.Getenv("LOOM_SECRET") != "" || os.Getenv("LOOM_SECRET_KEY_FILE") != "" {
		return errors.New("unset LOOM_SECRET/LOOM_SECRET_KEY_FILE to verify a backup: verification must use the keyring inside the archive")
	}
	s, err := openStore(dir)
	if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}
	defer s.Close()
	report, err := s.CheckHealth()
	if err != nil {
		return err
	}
	fmt.Printf("backup %s\n", *backup)
	fmt.Printf("  runs: %d (%d events), blobs indexed: %d, keys: %d\n",
		report.RunsChecked, report.EventsChecked, report.BlobsIndexed, report.KeyCount)
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
	if !report.OK() {
		return errors.New("backup verification failed")
	}
	fmt.Println("  all event chains verified; all blobs hash to their names")
	fmt.Println("backup OK")
	return nil
}

func runBackupRestore(args []string) error {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	backup := fs.String("backup", "", "archive to restore (required)")
	to := fs.String("to", "", "target directory (required, must not exist)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *backup == "" || *to == "" {
		return errors.New("--backup and --to are required")
	}
	if _, err := os.Stat(*to); err == nil {
		return fmt.Errorf("%s already exists", *to)
	}
	dir, err := extractBackup(*backup)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := moveOrCopyDir(dir, *to); err != nil {
		return err
	}
	fmt.Printf("restored %s to %s\n", *backup, *to)
	fmt.Println("open it in place with --data, or run loom doctor --data against it first")
	return nil
}

// extractBackup unpacks an archive into a fresh temp directory, rejecting any
// entry that would escape it (tar traversal).
func extractBackup(backup string) (string, error) {
	src, err := os.Open(backup)
	if err != nil {
		return "", err
	}
	defer src.Close()
	gz, err := gzip.NewReader(src)
	if err != nil {
		return "", fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	dir, err := os.MkdirTemp(".", "loom-restore-*")
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	allowed := func(name string) bool {
		if name == "loom.db" || name == "secret.key" || name == "chain.migrated" || name == "head.sealed" || name == "subprocess-policy.json" {
			return true
		}
		return strings.HasPrefix(name, "blobs/") || strings.HasPrefix(name, "keys/") || strings.HasPrefix(name, "envs/")
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		// Directory entries are optional in the format; tar tools that record
		// them (python tarfile, bsdtar re-packs) are accepted and skipped.
		if hdr.Typeflag == tar.TypeDir {
			if name == "blobs" || name == "keys" || name == "envs" ||
				strings.HasPrefix(name, "blobs/") || strings.HasPrefix(name, "keys/") || strings.HasPrefix(name, "envs/") {
				continue
			}
			os.RemoveAll(dir)
			return "", fmt.Errorf("unexpected archive directory %q", hdr.Name)
		}
		if !allowed(name) || strings.Contains(name, "..") {
			os.RemoveAll(dir)
			return "", fmt.Errorf("unexpected archive entry %q", hdr.Name)
		}
		dest := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.RemoveAll(dir)
			return "", err
		}
		out.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, "loom.db")); err != nil {
		os.RemoveAll(dir)
		return "", errors.New("archive has no loom.db")
	}
	return dir, nil
}

// moveOrCopyDir tries renaming src to dst first, and falls back to recursive copy
// if renaming across devices fails (EXDEV).
func moveOrCopyDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
