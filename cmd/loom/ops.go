package main

import (
	"encoding/json"
	"flag"
	"fmt"
)

// runOps implements `loom ops storage` (storage-operations campaign,
// D8-4): the same StorageStats the API serves, printed as JSON — one
// implementation, two transports. Runs against a stopped or live
// server's data directory (SQLite readers are safe alongside the
// writer).
func runOps(args []string) error {
	if len(args) == 0 || args[0] != "storage" {
		return fmt.Errorf("usage: loom ops storage [-data <dir>]")
	}
	fs := flag.NewFlagSet("ops storage", flag.ContinueOnError)
	data := fs.String("data", "data", "data directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	s, err := openStore(*data)
	if err != nil {
		return err
	}
	defer s.Close()

	stats, err := s.StorageStats()
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
