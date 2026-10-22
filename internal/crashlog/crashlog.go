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

type event struct {
	text  string
	clean bool
	done  chan struct{}
}

type logWriter struct {
	file         *os.File
	path, marker string
	events       chan event
	stop, done   chan struct{}
	size         int64
	started      time.Time
}

var (
	mu   sync.Mutex
	sink *logWriter
)

// Install arms the runtime crash writer. Breadcrumbs use a bounded background
// writer; fatal runtime output still bypasses this queue and goes to the file.
func Install(dir string) (string, error) {
	stopWriter()
	Maintain(dir)
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
	info, _ := f.Stat()
	w := &logWriter{file: f, path: path, marker: filepath.Join(logDir, sessionName),
		events: make(chan event, 256), stop: make(chan struct{}), done: make(chan struct{})}
	if info != nil {
		w.size = info.Size()
	}
	w.started = time.Now()
	if started, ok := logStarted(path); ok {
		w.started = started
	}
	mu.Lock()
	sink = w
	mu.Unlock()
	go w.run()
	if rotated {
		writef("上一份崩溃日志已轮转到 %s.1\n", fileName)
	}
	noteUnclean(w.marker)
	writeSession(w.marker)
	writef("===== session start pid=%d go=%s %s/%s =====\n%s\n", os.Getpid(), runtime.Version(), runtime.GOOS, runtime.GOARCH, platformHint())
	return path, nil
}

// Breadcrumb never waits for storage. A full queue drops diagnostic lines
// rather than blocking clipboard capture or retaining unbounded strings.
func Breadcrumb(msg string) {
	msg = oneLine(msg)
	if msg == "" {
		return
	}
	mu.Lock()
	w := sink
	if w != nil {
		select {
		case w.events <- event{text: time.Now().Format(time.RFC3339) + " " + msg + "\n"}:
		default:
		}
	}
	mu.Unlock()
}

// Record writes one diagnostic line and waits until it reaches disk.
// Startup uses it when the process may exit before an async breadcrumb is
// flushed. Hot-path capture must keep using Breadcrumb.
func Record(msg string) {
	msg = oneLine(msg)
	if msg == "" {
		return
	}
	sendEvent(event{text: time.Now().Format(time.RFC3339) + " " + msg + "\n"})
}

// MarkClean flushes pending diagnostics at normal exit. Unlike a hot-path
// breadcrumb, shutdown and explicit stack dumps wait for durable storage.
func MarkClean() {
	sendEvent(event{text: fmt.Sprintf("%s 正常退出 pid=%d\n", time.Now().Format(time.RFC3339), os.Getpid()), clean: true})
}

func sendEvent(e event) {
	mu.Lock()
	w := sink
	mu.Unlock()
	if w == nil {
		return
	}
	e.done = make(chan struct{})
	select {
	case w.events <- e:
		select {
		case <-e.done:
		case <-w.done:
		}
	case <-w.done:
	}
}

func (w *logWriter) run() {
	defer close(w.done)
	defer func() {
		if w.file != nil {
			_ = w.file.Close()
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	dirty := false
	write := func(e event) {
		if w.file != nil && (w.size >= rotateAt || w.expired()) {
			w.rotate()
		}
		if w.file == nil {
			if e.done != nil {
				close(e.done)
			}
			return
		}
		n, _ := w.file.WriteString(e.text)
		w.size += int64(n)
		dirty = true
		if e.done != nil {
			_ = w.file.Sync()
			dirty = false
			if e.clean {
				_ = os.Remove(w.marker)
			}
			close(e.done)
		}
	}
	for {
		select {
		case e := <-w.events:
			write(e)
		case <-tick.C:
			if dirty && w.file != nil {
				_ = w.file.Sync()
				dirty = false
			}
		case <-w.stop:
			for {
				select {
				case e := <-w.events:
					write(e)
				default:
					if w.file != nil {
						_ = w.file.Sync()
					}
					return
				}
			}
		}
	}
}

func (w *logWriter) expired() bool {
	return !w.started.IsZero() && time.Since(w.started) >= retainFor
}

func (w *logWriter) rotate() {
	aged := w.expired()
	if w.file != nil {
		_ = w.file.Sync()
	}
	// The runtime duplicates its file descriptor; release it before renaming
	// on Windows, then point crash output at the replacement file.
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	moved := false
	if aged {
		moved = renameAside(w.path, 0)
		if moved {
			_ = os.Remove(w.path + ".1")
		}
	} else {
		moved = rotate(w.path)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	w.file = f
	info, err := f.Stat()
	if err == nil {
		w.size = info.Size()
	} else {
		w.size = 0
	}
	if moved {
		w.started = time.Now()
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
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

func writef(format string, args ...any) { sendEvent(event{text: fmt.Sprintf(format, args...)}) }

func rotate(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < rotateAt {
		return false
	}
	if !renameAside(path, info.Size()) {
		return false
	}
	// An oversized file is moved aside and then deleted. Do not report that
	// a backup was kept.
	if _, err := os.Stat(path + ".1"); err != nil {
		return false
	}
	return true
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

// resetForTest stops the process-wide writer and joins it before closing files.
func stopWriter() {
	mu.Lock()
	w := sink
	sink = nil
	mu.Unlock()
	if w != nil {
		close(w.stop)
		<-w.done
	}
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
}

func resetForTest() { stopWriter() }
