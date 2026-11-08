module github.com/kanggu12/wsl-shrinker

go 1.26.0

// The newest patch when this was written: it carries the standard library's
// security fixes, so an older Go fetches it before building. CI and the
// release build take the newest 1.26 patch whatever this says.
toolchain go1.26.10

// The first versions published were numbered past where the releases now
// start; v0.4.1 exists only to carry this list to the module proxy, which
// reads retractions from the highest version it has.
retract (
	v0.4.1 // Carries these retractions only.
	v0.4.0 // Published before the version numbers started over at v0.1.0.
	v0.3.0 // Published before the version numbers started over at v0.1.0.
)

require (
	charm.land/bubbletea/v2 v2.1.1
	charm.land/lipgloss/v2 v2.0.6
	github.com/BurntSushi/toml v1.6.0
	github.com/charmbracelet/x/ansi v0.11.9
	github.com/charmbracelet/x/term v0.2.2
	github.com/spf13/cobra v1.10.2
	golang.org/x/sys v0.47.0
)

require (
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261008173134-6b8d4baf91b4 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xo/terminfo v1.0.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
)
