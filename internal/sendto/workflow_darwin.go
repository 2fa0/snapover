//go:build darwin

package sendto

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

const workflowName = "Snapover 发送文件.workflow"

//go:embed workflow_info.plist
var workflowInfo []byte

//go:embed workflow_document.wflow
var workflowDocument string

func workflowPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Services", workflowName), nil
}

func Install(ctx context.Context) error {
	path, err := workflowPath()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if filepath.Base(filepath.Dir(exe)) != "MacOS" || filepath.Base(filepath.Dir(filepath.Dir(exe))) != "Contents" || !strings.HasSuffix(filepath.Dir(filepath.Dir(filepath.Dir(exe))), ".app") {
		return errors.New("请先将 Snapover.app 安装到「应用程序」，启动后重新安装快速操作")
	}
	return createWorkflow(ctx, path, exe)
}

func Remove() error {
	path, err := workflowPath()
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func createWorkflow(ctx context.Context, path, exe string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resources := filepath.Join(path, "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o700); err != nil {
		return err
	}
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(workflowScript(exe))); err != nil {
		return err
	}
	document := strings.Replace(workflowDocument, "{{SCRIPT}}", escaped.String(), 1)
	for _, file := range []struct {
		path string
		data []byte
	}{
		{filepath.Join(path, "Contents", "Info.plist"), workflowInfo},
		{filepath.Join(resources, "document.wflow"), []byte(document)},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := os.ReadFile(file.path)
		if err == nil && bytes.Equal(current, file.data) {
			continue
		}
		if err := writeWorkflowFile(file.path, file.data); err != nil {
			return err
		}
	}
	return nil
}

func writeWorkflowFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".snapover-workflow-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func workflowScript(exe string) string {
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "'"
	// Detach all three streams so Automator can finish while the app sends.
	return "[ \"$#\" -gt 0 ] || exit 0\n" +
		"if [ ! -x " + quoted + " ]; then\n" +
		"  printf '%s\\n' '找不到 Snapover，请启动应用并在设置中重新安装快速操作。' >&2\n" +
		"  exit 1\nfi\n" +
		quoted + " --send-file \"$@\" </dev/null >/dev/null 2>&1 &\n"
}

var refreshServices = sync.OnceValues(func() (func(), error) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	address, err := purego.Dlsym(lib, "NSUpdateDynamicServices")
	if err != nil {
		return nil, err
	}
	var refresh func()
	purego.RegisterFunc(&refresh, address)
	return refresh, nil
})

// Refresh asks macOS to discover added or removed workflows. Call on the main thread.
func Refresh() error {
	refresh, err := refreshServices()
	if err == nil {
		refresh()
	}
	return err
}
