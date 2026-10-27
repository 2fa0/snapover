//go:build !darwin && !windows

package clip

import "github.com/egoist/mygo"

func readClipboardFiles() ([]string, error) { return mygo.Clipboard.ReadFiles() }
