//go:build !darwin && !windows

package filetransfer

import "os"

func publishFile(from, to string) error {
	return os.Link(from, to)
}
