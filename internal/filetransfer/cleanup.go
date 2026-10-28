package filetransfer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const staleTransferAge = 24 * time.Hour

// Keep a paused live receive even if its timestamp is older than the cutoff.
var activeParts sync.Map

func partKey(path string) string {
	if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(dir, filepath.Base(path))
	}
	return path
}

// CleanStaleTransfers removes only our expired private staging directories.
// Legacy loose .part files cannot be distinguished from saved files by name.
func CleanStaleTransfers(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-staleTransferAge)
	var failures []error
	for _, entry := range entries {
		number, ok := strings.CutPrefix(entry.Name(), ".snapover-incoming-")
		if !ok || number == "" || strings.Trim(number, "0123456789") != "" || !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if _, live := activeParts.Load(partKey(path)); live {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		children, err := os.ReadDir(path)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		// Do not recurse into unexpected contents, even in a matching directory.
		if len(children) > 1 || (len(children) == 1 && (children[0].Name() != "contents.part" || !children[0].Type().IsRegular())) {
			continue
		}
		if len(children) == 1 {
			if err := os.Remove(filepath.Join(path, "contents.part")); err != nil && !os.IsNotExist(err) {
				failures = append(failures, err)
				continue
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
