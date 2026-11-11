// Package filetransfer stages incoming files without exposing partial content.
package filetransfer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/zeebo/blake3"
)

func Validate(path string, maxBytes int64) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取文件：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("请选择一个普通文件，不支持文件夹")
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("文件超过 %d MB，请在设置中调大单文件大小上限或选择更小的文件", maxBytes>>20)
	}
	return info, nil
}

// ValidatePaths checks an explicit send batch and returns each size in order.
// Explicit sending stays per-file: every entry must be a regular file within the
// limit, and the sizes are only used for display and batch progress.
func ValidatePaths(paths []string, maxBytes int64) ([]int64, error) {
	if len(paths) == 0 {
		return nil, errors.New("请选择要发送的文件")
	}
	sizes := make([]int64, len(paths))
	var total int64
	for i, path := range paths {
		info, err := Validate(path, maxBytes)
		if err != nil {
			if len(paths) == 1 {
				return nil, err
			}
			return nil, fmt.Errorf("%s：%w", filepath.Base(path), err)
		}
		if info.Size() > math.MaxInt64-total {
			return nil, errors.New("文件批次总大小超出可表示范围，请分批发送")
		}
		total += info.Size()
		sizes[i] = info.Size()
	}
	return sizes, nil
}

// SafeName applies the filename restrictions of both supported platforms.
func SafeName(name string) (string, error) {
	if !utf8.ValidString(name) || name == "" || len(name) > 240 || strings.ContainsAny(name, "/\\") {
		return "", errors.New("文件名无效")
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune("<>:\"|?*", r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, " .")
	if name == "" || name == "." || name == ".." {
		return "", errors.New("文件名无效")
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" ||
		(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		name = "_" + name
	}
	return name, nil
}

type Incoming struct {
	file          *os.File
	hash          *blake3.Hasher
	dir, name     string
	stageDir      string
	Size, Written int64
}

func Begin(dir, name string, size, maxBytes int64) (*Incoming, error) {
	if size < 0 || size > maxBytes {
		return nil, fmt.Errorf("文件超过接收上限 %d MB 或大小无效，请在接收端设置中调整单文件大小上限", maxBytes>>20)
	}
	name, err := SafeName(name)
	if err != nil {
		return nil, err
	}
	if err := CheckReceiveDir(dir); err != nil {
		return nil, err
	}
	if err := checkDiskSpace(dir, size); err != nil {
		return nil, err
	}
	stageDir, err := os.MkdirTemp(dir, ".snapover-incoming-")
	if err != nil {
		return nil, fmt.Errorf("无法创建接收暂存目录：%w", err)
	}
	f, err := os.OpenFile(filepath.Join(stageDir, "contents.part"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = os.Remove(stageDir)
		return nil, fmt.Errorf("无法写入接收文件夹：%w", err)
	}
	activeParts.Store(partKey(stageDir), true)
	return &Incoming{file: f, hash: blake3.New(), dir: dir, name: name, stageDir: stageDir, Size: size}, nil
}

// CheckReceiveDir rejects volumes without a safe publish operation before
// accepting a transfer or saving the user's choice of directory.
func CheckReceiveDir(dir string) error {
	if dir == "" || !filepath.IsAbs(dir) {
		return errors.New("接收文件夹未设置")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("无法创建接收文件夹：%w", err)
	}
	// Cleanup failure must not prevent receiving a new file in a writable folder.
	_ = CleanStaleTransfers(dir)
	f, err := os.CreateTemp(dir, ".snapover-check-*.part")
	if err != nil {
		return fmt.Errorf("无法写入接收文件夹：%w", err)
	}
	defer os.Remove(f.Name())
	target := f.Name() + ".saved"
	if err := f.Close(); err != nil {
		return err
	}
	if err := publishFile(f.Name(), target); err != nil {
		return fmt.Errorf("接收文件夹不支持安全保存，请选择其他文件夹：%w", err)
	}
	defer os.Remove(target)
	return nil
}

func (f *Incoming) Append(offset int64, data []byte) error {
	if offset != f.Written || len(data) == 0 || int64(len(data)) > f.Size-f.Written {
		return errors.New("文件分片顺序或大小无效")
	}
	if n, err := f.file.Write(data); err != nil || n != len(data) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return fmt.Errorf("写入文件失败：%w", err)
	}
	_, _ = f.hash.Write(data)
	f.Written += int64(len(data))
	return nil
}

func (f *Incoming) Abort() {
	_ = f.file.Close()
	_ = os.Remove(f.file.Name())
	_ = os.Remove(f.stageDir)
	activeParts.Delete(partKey(f.stageDir))
}

func (f *Incoming) Finish(hash []byte) (string, error) {
	defer f.Abort()
	if f.Written != f.Size || len(hash) != 32 || !bytes.Equal(hash, f.hash.Sum(nil)) {
		return "", errors.New("文件不完整或校验失败，请重新发送")
	}
	if err := f.file.Sync(); err != nil {
		return "", err
	}
	if err := f.file.Close(); err != nil {
		return "", err
	}
	ext := filepath.Ext(f.name)
	stem := strings.TrimSuffix(f.name, ext)
	for i := 0; i < 10000; i++ {
		name := f.name
		if i > 0 {
			name = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		path := filepath.Join(f.dir, name)
		if err := publishFile(f.file.Name(), path); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", fmt.Errorf("保存文件失败：%w", err)
		}
		return path, nil
	}
	return "", errors.New("同名文件过多，请清理接收文件夹")
}
