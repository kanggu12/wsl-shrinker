//go:build !windows

package winapi

// Off Windows there is no PATH in the registry and no Windows program to
// remove: an uninstall is refused before it asks these.

// UserPath has no registry to read off Windows.
func UserPath() (string, bool, error) { return "", false, ErrNotWindows }

// SetUserPath has no registry to write off Windows.
func SetUserPath(string, bool) error { return ErrNotWindows }

// InUse cannot say off Windows: no file is held.
func InUse(string) bool { return false }

// OthersRunning cannot say off Windows: none are counted.
func OthersRunning(string) (int, error) { return 0, nil }

// DeleteAfterExit has no cmd.exe off Windows.
func DeleteAfterExit(string, string) error { return ErrNotWindows }
