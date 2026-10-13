package control

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDataDir is shared by the GUI and CLI, including portable Windows builds.
func DefaultDataDir() (string, error) {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, "portable.flag")); err == nil {
			return filepath.Join(dir, "data"), nil
		}
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Snapover"), nil
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", fmt.Errorf("APPDATA 未设置")
		}
		return filepath.Join(base, "Snapover"), nil
	default:
		return "", fmt.Errorf("不支持 %s", runtime.GOOS)
	}
}
