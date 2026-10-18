// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the MiryMesh CLI for administrative and migration tasks.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("mirymesh version dev")
		return
	}
	fmt.Fprintln(os.Stderr, "usage: mirymesh [version|help]")
	if len(os.Args) > 1 && os.Args[1] == "help" {
		fmt.Println("MiryMesh CLI — see docs/ for usage.")
		return
	}
}
