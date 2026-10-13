package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/snapover/internal/control"
)

func TestParseTextAndStdinPreserveContent(t *testing.T) {
	for _, test := range []struct {
		args        []string
		stdin, want string
	}{
		{[]string{"send", "text", "--data", "unused", "  中文\r\n末尾\n"}, "", "  中文\r\n末尾\n"},
		{[]string{"send", "text", "--data", "unused", "--stdin"}, "\ufeff空行\n\n", "空行\n\n"},
		{[]string{"send", "text", "--data", "unused", "--", "-literal"}, "", "-literal"},
	} {
		cmd, err := parse(context.Background(), test.args, strings.NewReader(test.stdin))
		if err != nil || cmd.request.Value != test.want {
			t.Fatalf("%+v %v", cmd, err)
		}
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"send", "folder", "x"}, {"send", "text", ""}, {"send", "text", "a", "b"},
		{"send", "file", "--stdin"}, {"send", "text", "--stdin", "extra"},
		{"send", "text", "--timeout", "0", "x"}, {"send", "text", "--timeout", "11m", "x"},
		{"send", "text", "--data", "unused", "\xff"},
	} {
		if _, err := parse(context.Background(), args, strings.NewReader("x")); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
	for _, data := range []string{strings.Repeat("a", (1<<20)+1), "\ufeff" + strings.Repeat("a", 1<<20)} {
		if _, err := parse(context.Background(), []string{"send", "text", "--stdin", "--data", "unused"}, strings.NewReader(data)); err == nil {
			t.Fatal("oversize input silently truncated")
		}
	}
}

func TestParseFilePathsAndLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "中文 文件.txt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, err := parse(context.Background(), []string{"send", "file", "--data", dir, path}, nil)
	if err != nil || cmd.request.Value != path {
		t.Fatalf("%+v %v", cmd, err)
	}
	if _, err := parse(context.Background(), []string{"send", "file", "--data", dir, dir}, nil); err == nil {
		t.Fatal("directory accepted")
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate((50 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := parse(context.Background(), []string{"send", "file", "--data", dir, path}, nil); err == nil {
		t.Fatal("oversize file accepted")
	}
}

func TestRunExitCodesAndOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	s, err := control.Start(ctx, dir, func(_ context.Context, request control.Request, _ func(int64, int64)) (string, error) {
		return "已写入：" + request.Value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out, errs bytes.Buffer
	if code := run(ctx, []string{"send", "text", "--data", dir, "你好"}, nil, &out, &errs); code != 0 || !strings.Contains(out.String(), "已写入：你好") {
		t.Fatalf("%d %s %s", code, out.String(), errs.String())
	}
	if code := run(ctx, []string{"send", "text", "--data", t.TempDir(), "x"}, nil, &out, &errs); code != 1 {
		t.Fatalf("%d", code)
	}
	if code := run(ctx, []string{"send", "text"}, nil, &out, &errs); code != 2 {
		t.Fatalf("%d", code)
	}
	if code := run(ctx, []string{"--help"}, nil, &out, &errs); code != 0 {
		t.Fatalf("%d", code)
	}
}
