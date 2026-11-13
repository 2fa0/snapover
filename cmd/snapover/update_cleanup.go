package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// An update unpacks itself next to the app, in a .<name>.update-<random>
// directory, and removes it when it is done — unless the app is killed,
// crashes, or quits in the middle, which leaves the archive and the unpacked
// app there for good: up to a gigabyte, and the updater only clears what it
// renamed aside (.old-<pid>) at launch. Nothing can be using one of these by
// the time the app has run for a minute: the helper that installs a staged
// update on macOS finishes before that.
const (
	updateLeftoverAge   = 24 * time.Hour
	updateLeftoverDelay = time.Minute
)

// cleanUpdateLeftovers deletes the update directories of earlier runs that
// nothing has touched for a day, once no helper of the previous run can still
// be installing from one.
func (a *app) cleanUpdateLeftovers() {
	dir := appDir()
	if dir == "" {
		return
	}
	time.AfterFunc(updateLeftoverDelay, func() {
		now := time.Now()
		for _, path := range staleUpdateDirs(dir, now, updateLeftoverAge) {
			if err := os.RemoveAll(path); err != nil && a.engine != nil && a.engine.Log != nil {
				a.engine.Log.Warn("清理更新残留目录失败", "path", path, "err", err)
			}
		}
	})
}

// appDir returns the directory that holds the app, which an update unpacks
// itself beside: the parent of the app bundle on macOS, as the updater
// replaces the bundle, and the directory of the executable elsewhere.
func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return appDirOf(exe, runtime.GOOS)
}

// appDirOf returns the directory that holds the app of executable exe, or ""
// when exe is not in one.
func appDirOf(exe, goos string) string {
	if goos == "darwin" {
		// …/Snapover.app/Contents/MacOS/Snapover
		bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
		if !strings.HasSuffix(bundle, ".app") {
			return ""
		}
		return filepath.Dir(bundle)
	}
	return filepath.Dir(exe)
}

// staleUpdateDirs returns the update directories of dir that are older than
// age, and so belong to no update under way.
func staleUpdateDirs(dir string, now time.Time, age time.Duration) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var stale []string
	for _, entry := range entries {
		if !entry.IsDir() || !isUpdateWorkDir(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < age {
			continue
		}
		stale = append(stale, filepath.Join(dir, entry.Name()))
	}
	return stale
}

// isUpdateWorkDir reports whether name is a directory the updater unpacks an
// update in, from os.MkdirTemp's ".{app}.update-" pattern, or one of its
// probes of whether the app can write there. What it renamed aside,
// ".{name}.old-{pid}", is the updater's to remove.
func isUpdateWorkDir(name string) bool {
	return strings.HasPrefix(name, ".") &&
		(strings.Contains(name, ".update-") || strings.HasPrefix(name, ".mygo-update-"))
}
