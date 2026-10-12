package filetransfer

import "golang.org/x/sys/unix"

// Publish without requiring hard-link support, while preserving existing files.
func publishFile(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}
