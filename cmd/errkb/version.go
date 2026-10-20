//go:build windows

package main

import (
	"fmt"
	"io"
	"runtime"
)

// The three values below are stamped by packaging/windows/build.ps1 through
// `-ldflags -X`; the defaults describe a local `go build`. Build time is the
// commit's own timestamp (not the clock), so the same source builds the same
// binary on every machine.
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

// printVersion writes exactly the fields packaging/windows/versioninfo.json
// records: the version document can be compared line by line with this output.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "errkb %s\n", version)
	fmt.Fprintf(w, "commit %s\n", commit)
	fmt.Fprintf(w, "built %s\n", buildTime)
	fmt.Fprintf(w, "go %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
