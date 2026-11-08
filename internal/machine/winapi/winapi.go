// Package winapi holds the few Windows calls this tool needs about files and
// processes: how much space a file takes, its attributes, the free space of
// its drive, whether WSL holds it, and whether this process was started from
// inside WSL. Off Windows the calls report that they cannot answer.
package winapi

import "errors"

// ErrNotWindows is returned off Windows.
var ErrNotWindows = errors.New("not on Windows")

// FileInfo is what Windows says about a disk file.
type FileInfo struct {
	// Size is the file's length; OnDisk what it takes on the drive (less for
	// a sparse or compressed file).
	Size   int64 `json:"size_bytes"`
	OnDisk int64 `json:"on_disk_bytes"`
	// Sparse, Compressed and Encrypted are the file's NTFS attributes. WSL
	// cannot compact an encrypted disk (0x80071772).
	Sparse     bool `json:"sparse"`
	Compressed bool `json:"compressed"`
	Encrypted  bool `json:"encrypted"`
	// Held is set when another process has the file open for writing: WSL's
	// VM holds a disk while it is attached.
	Held bool `json:"held"`
	// DriveFree is the free space of the file's drive.
	DriveFree int64 `json:"drive_free_bytes"`
}
