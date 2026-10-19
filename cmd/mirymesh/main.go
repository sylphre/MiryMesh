// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the MiryMesh CLI for administrative and migration tasks.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

func main() {
	// mirymesh backup/restore are local utility commands for master takeover.
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("mirymesh version dev")
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "backup" || os.Args[1] == "evacuate") {
		runBackup()
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "restore" || os.Args[1] == "adopt") {
		runRestore()
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Println("MiryMesh CLI — disaster recovery")
		fmt.Println("  mirymesh version                         — print version")
		fmt.Println("  mirymesh backup [--dir DIR] [--storage DIR] — write evacuation snapshot from local storage")
		fmt.Println("  mirymesh restore [--dir DIR] [--storage DIR] [remoteDir...] — restore buckets from newest snapshot")
		return
	}
	fmt.Fprintln(os.Stderr, "usage: mirymesh [version|help|backup|restore]")
	if len(os.Args) > 1 {
		os.Exit(1)
	}
}

func parseFlag(args []string, name string) (string, []string) {
	for i, a := range args {
		if a == "--"+name && i+1 < len(args) {
			return args[i+1], append(args[:i], args[i+2:]...)
		}
		if len(a) > 3+len(name) && a[:3+len(name)] == "--"+name+"=" {
			val := a[3+len(name):]
			rest := append(args[:i], args[i+1:]...)
			return val, rest
		}
	}
	return "", args
}

func runBackup() {
	args := os.Args[2:]
	dir, _ := parseFlag(args, "dir")
	if dir == "" {
		dir = "./mirymesh-evac"
	}
	storageDir, _ := parseFlag(args, "storage")
	if storageDir == "" {
		storageDir = "/var/lib/mirymesh"
	}
	// Discover buckets/objects from storage dir.
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: read storage dir: %v\n", err)
		os.Exit(1)
	}
	var buckets []string
	var objects []metadata.ObjectRecord
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		buckets = append(buckets, e.Name())
		// Walk objects under bucket.
		bktDir := filepath.Join(storageDir, e.Name())
		_ = filepath.WalkDir(bktDir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(bktDir, path)
			key := filepath.ToSlash(rel)
			fi2, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			objects = append(objects, metadata.ObjectRecord{Bucket: e.Name(), Key: key, Size: fi2.Size()})
			return nil
		})
	}
	ev2, evacErr := metadata.NewEvacuator(dir)
	if evacErr != nil {
		fmt.Fprintf(os.Stderr, "backup: evacuator: %v\n", evacErr)
		os.Exit(1)
	}
	if saveErr := ev2.Save(&metadata.EvacuationSnapshot{Buckets: buckets, Objects: objects}); saveErr != nil {
		fmt.Fprintf(os.Stderr, "backup: save: %v\n", saveErr)
		os.Exit(1)
	}
	slog.Info("backup: evacuation snapshot saved", "dir", dir, "buckets", len(buckets), "objects", len(objects))
	fmt.Printf("backup: %d buckets, %d objects -> %s\n", len(buckets), len(objects), dir)
}

func runRestore() {
	args := os.Args[2:]
	dir, _ := parseFlag(args, "dir")
	if dir == "" {
		dir = "./mirymesh-evac"
	}
	storageDir, _ := parseFlag(args, "storage")
	if storageDir == "" {
		storageDir = "/var/lib/mirymesh"
	}
	// Remaining args are remoteDir... for LoadAny.
	remoteArgs := args
	// parseFlag already strips recognized flags; residual args are remote dirs.
	// Re-parse: anything not starting with -- is a remoteDir.
	var remotes []string
	for _, a := range remoteArgs {
		if a != "" && a[0] != '-' {
			remotes = append(remotes, a)
		}
	}
	snap, err := metadata.LoadAny(dir, remotes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: load snapshot: %v\n", err)
		os.Exit(1)
	}
	restorer, err := metadata.NewRestorer(storageDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: restorer: %v\n", err)
		os.Exit(1)
	}
	res, err := restorer.Restore(snap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("restore: version=%d buckets created=%d restored=%d objects present=%d missing=%d\n", res.Version, res.BucketsCreated, res.BucketsRestored, res.ObjectsPresent, res.ObjectsMissing)
	if len(res.MissingKeys) > 0 {
		fmt.Println("missing keys:")
		for _, k := range res.MissingKeys {
			fmt.Printf("  %s\n", k)
		}
	}
}
