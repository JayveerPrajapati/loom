package main

import (
	"flag"
	"fmt"
	"os"
)

func runKey(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: loom key <rotate|list> [--data DIR]")
	}
	fs := flag.NewFlagSet("loom key "+args[0], flag.ContinueOnError)
	data := fs.String("data", "data", "data directory")
	if err := fs.Parse(args[1:]); err != nil {
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
	switch args[0] {
	case "rotate":
		id, err := s.RotateKey()
		if err != nil {
			return err
		}
		fmt.Printf("rotated signing key: now using %s\n", id)
		fmt.Println("previous keys remain in the keyring so recorded runs keep verifying")
		return nil
	case "list":
		for _, k := range s.ListKeys() {
			mark := "        "
			switch {
			case k.Current:
				mark = "current "
			case k.Legacy:
				mark = "legacy  "
			}
			fmt.Printf("%s%s\n", mark, k.ID)
		}
		return nil
	default:
		return fmt.Errorf("unknown key command %q (want rotate or list)", args[0])
	}
}
