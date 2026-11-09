//go:build !windows

package clip

// OwnerProcess is only meaningful on Windows, where another process can steal
// the clipboard after a successful write.
func OwnerProcess() string { return "" }
