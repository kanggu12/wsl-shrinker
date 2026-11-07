//go:build !windows

package main

import (
	"slices"
	"testing"
)

func TestWSLENV(t *testing.T) {
	// Windows Terminal sets WSLENV already; the entry is added once.
	if got := appendWSLENV("WT_SESSION:WT_PROFILE_ID:", "WSL_DISTRO_NAME/w"); got != "WT_SESSION:WT_PROFILE_ID::WSL_DISTRO_NAME/w" {
		t.Fatalf("%q", got)
	}
	if got := appendWSLENV("WSL_DISTRO_NAME/w", "WSL_DISTRO_NAME/w"); got != "WSL_DISTRO_NAME/w" {
		t.Fatalf("%q", got)
	}
	// The environment holds one WSLENV, the new one.
	env := withWSLENV([]string{"A=1", "WSLENV=old", "B=2"}, "new")
	n := 0
	for _, e := range env {
		if len(e) > 7 && e[:7] == "WSLENV=" {
			n++
		}
	}
	if n != 1 || !slices.Contains(env, "WSLENV=new") || !slices.Contains(env, "B=2") {
		t.Fatalf("%v", env)
	}
}
