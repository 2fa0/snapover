// Package crashlog records process crashes next to the ordinary log.
//
// Go panics and runtime fatal errors are written to logs/crash.log by
// debug.SetCrashOutput. A native abort that never returns to Go leaves no
// stack; the session marker and the last breadcrumb still show that it died
// and what it was doing.
package crashlog

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const (
	fileName    = "crash.log"
	sessionName = "session.open"
	rotateAt    = 1 << 20
)

var (
	mu          sync.Mutex
	file        *os.File
	sessionPath string
)

// Install opens logs/crash.log under dir and arms the runtime crash writer.
// A previous session that did not call MarkClean is noted in the new file.
func Install(dir string) (string, error) {
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(logDir, fileName)
	rotated := rotate(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		return "", err
	}
	debug.SetTraceback("all")

	mu.Lock()
	if file != nil && file != f {
		file.Close()
	}
	file = f
	sessionPath = filepath.Join(logDir, sessionName)
	mu.Unlock()

	if rotated {
		writef("上一份崩溃日志已轮转到 %s.1\n", fileName)
	}
	noteUnclean(sessionPath)
	writeSession(sessionPath)
	writef("===== session start pid=%d go=%s %s/%s =====\n%s\n",
		os.Getpid(), runtime.Version(), runtime.GOOS, runtime.GOARCH, platformHint())
	return path, nil
}

// Breadcrumb appends one synced line. It is a no-op before Install.
// Do not put clipboard contents here; format names and sequence numbers only.
func Breadcrumb(msg string) {
	msg = oneLine(msg)
	if msg == "" {
		return
	}
	writef("%s %s\n", time.Now().Format(time.RFC3339), msg)
}

// MarkClean records a normal exit so the next start does not treat it as a crash.
// Safe to call more than once, and safe when Install was never called.
// Do not call it from a defer that also runs while a panic unwinds: that
// would hide the crash. Call it only on paths that are actually leaving cleanly.
func MarkClean() {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return
	}
	fmt.Fprintf(file, "%s 正常退出 pid=%d\n", time.Now().Format(time.RFC3339), os.Getpid())
	_ = file.Sync()
	if sessionPath != "" {
		_ = os.Remove(sessionPath)
		sessionPath = ""
	}
}

func noteUnclean(marker string) {
	buf, err := os.ReadFile(marker)
	if err != nil {
		return
	}
	text := strings.TrimSpace(string(buf))
	if text == "" {
		text = "(没有上次的进程信息)"
	}
	writef("----- 上次没有正常退出 -----\n%s\n上面最后一条面包屑是崩溃前的位置。%s\n-----\n", text, platformHint())
}

func writeSession(marker string) {
	body := fmt.Sprintf("pid=%d\nstarted=%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
	_ = os.WriteFile(marker, []byte(body), 0o600)
}

func writef(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return
	}
	fmt.Fprintf(file, format, args...)
	_ = file.Sync()
}

func rotate(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() < rotateAt {
		return false
	}
	_ = os.Remove(path + ".1")
	return os.Rename(path, path+".1") == nil
}

func oneLine(msg string) string {
	msg = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(msg, "\r", " "), "\n", " "))
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return msg
}

func platformHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "若这里没有栈，是原生闪退，再看 ~/Library/Logs/DiagnosticReports 里 Snapover 开头的报告。"
	case "windows":
		return "若这里没有栈，是原生闪退，再看事件查看器里的应用程序错误。"
	default:
		return ""
	}
}

// resetForTest drops the process-wide crash file. Tests only.
func resetForTest() {
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	mu.Lock()
	if file != nil {
		file.Close()
		file = nil
	}
	sessionPath = ""
	mu.Unlock()
}
