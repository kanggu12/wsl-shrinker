//go:build !windows

package wsl

import "context"

// Real is the System of this PC; off Windows every call fails.
type Real struct{}

// New returns the System of this PC.
func New() System { return Real{} }

// Distros fails off Windows.
func (Real) Distros(context.Context) ([]Distro, error) { return nil, ErrNotWindows }

// Info fails off Windows.
func (Real) Info(context.Context) (Info, error) { return Info{}, ErrNotWindows }

// Run fails off Windows.
func (Real) Run(context.Context, string, string, string) (string, error) { return "", ErrNotWindows }

// Start fails off Windows.
func (Real) Start(context.Context, string) error { return ErrNotWindows }

// Terminate fails off Windows.
func (Real) Terminate(context.Context, string) error { return ErrNotWindows }

// Compact fails off Windows.
func (Real) Compact(context.Context, string) error { return ErrNotWindows }
