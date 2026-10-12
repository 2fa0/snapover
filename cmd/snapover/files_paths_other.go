//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

func defaultReceiveDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Downloads", "Snapover")
}
