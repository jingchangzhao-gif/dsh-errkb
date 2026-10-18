// Command errkb is the errkb toolbox: it captures failures from LLM tools,
// records a diagnosis once, and hands the known fix back the next time the same
// failure appears.
//
// This file is the EKB-005 skeleton. It only proves the Go toolchain is wired
// up; the daemon, run, search, mcp and doctor subcommands arrive in later cards.
package main

import (
	"fmt"
	"runtime"
)

// version is stamped by packaging/windows/build.ps1; "dev" is a local build.
var version = "dev"

func main() {
	fmt.Printf("errkb %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
}
