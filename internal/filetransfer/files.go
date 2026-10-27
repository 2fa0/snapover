// Package filetransfer stages incoming files without exposing partial content.
package filetransfer

import (
	"bytes"
	"errors"
	"fmt"
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
	f, err := os.CreateTemp(dir, ".snapover-*.part")
	if err != nil {
		return nil, fmt.Errorf("无法写入接收文件夹：%w", err)
	}
	return &Incoming{file: f, hash: blake3.New(), dir: dir, name: name, Size: size}, nil
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
	if _, err := f.file.Write(data); err != nil {
		return fmt.Errorf("写入文件失败：%w", err)
	}
	_, _ = f.hash.Write(data)
	f.Written += int64(len(data))
	return nil
}

func (f *Incoming) Abort() {
	_ = f.file.Close()
	_ = os.Remove(f.file.Name())
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
