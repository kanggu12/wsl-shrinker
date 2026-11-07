// Command wsl-shrinker shows where WSL disk space went, cleans what the
// person chooses, shrinks the disks and checks that it worked.
package main

import (
	"os"

	"github.com/kanggu12/wsl-shrinker/internal/front/cli"
)

func main() {
	// Inside WSL the Linux build hands over to the Windows one.
	forward()
	os.Exit(cli.Execute())
}
