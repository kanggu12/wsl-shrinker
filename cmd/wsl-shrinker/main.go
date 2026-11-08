// Command wsl-shrinker shows where WSL disk space went, cleans what the
// person chooses, shrinks the disks and checks that it worked.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/kanggu12/wsl-shrinker/internal/front/cli"
	"github.com/kanggu12/wsl-shrinker/internal/machine/winapi"
)

// finish ends a run that watched for crashes: it removes the run's crash
// file and says where an earlier run's crash was kept. Every way out of a
// run that watched goes through it.
var finish = func() {}

// leave ends the program: the last words, then the console as the shell
// had it (the words take the colors it was given), then the exit code.
func leave(code int) {
	finish()
	winapi.RestoreConsole()
	os.Exit(code)
}

func main() {
	// A bug says so in the program's own words, with what a report needs,
	// rather than as a bare Go panic. A run's own steps recover on their
	// own and leave the disk as the journal says; this is the last word.
	defer func() {
		if r := recover(); r != nil {
			cli.PrintError(os.Stderr, fmt.Sprintf("stopped on a bug: %v", r), "please report it with the lines below: "+cli.IssuesURL)
			// Where it happened, for the report.
			os.Stderr.Write(debug.Stack())
			leave(1)
		}
	}()
	// The marks the terminal can show, before anything is drawn: plain ones
	// in the Windows console's own window, whose fonts lack the others.
	cli.SetMarks()
	// Inside WSL the Linux build hands over to the Windows one, which keeps
	// its own crashes; this one watches only what it runs itself.
	forward()
	// A crash no recover can catch is kept in a file for a later start.
	finish = cli.WatchCrashes()
	leave(cli.Execute())
}
