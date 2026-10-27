package clip

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/zeebo/blake3"
)

const MaxFilePaths = 10000
const MaxFileListBytes = 1 << 20

func HasFiles(formats []string) bool {
	for _, f := range formats {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "application/x-mygo-file-list", "public.file-url", "cf_hdrop", "hdrop":
			return true
		}
	}
	return false
}

// FromFiles captures references only. Disk reads belong to the file worker.
func FromFiles(paths []string) (Item, error) {
	if len(paths) == 0 || len(paths) > MaxFilePaths {
		return Item{}, errors.New("剪贴板文件数量无效或超过 10000 项")
	}
	size := 0
	hash := blake3.New()
	for _, p := range paths {
		size += len(p) + 1
		if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) || size > MaxFileListBytes {
			return Item{}, errors.New("剪贴板文件路径无效或过长")
		}
		_, _ = hash.Write([]byte(p))
		_, _ = hash.Write([]byte{0})
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return Item{Kind: KindFiles, Files: append([]string(nil), paths...), Size: size, Hash: sum}, nil
}

// FilePaths avoids decoding unrelated clipboard images during cache cleanup.
func (MyGoBoard) FilePaths() ([]string, error) { return readClipboardFiles() }
