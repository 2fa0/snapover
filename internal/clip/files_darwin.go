//go:build darwin

package clip

import (
	"github.com/egoist/mygo/transfer"
)

func readClipboardFiles() ([]string, error) {
	entries, err := readNativePasteboardItems("public.file-url", MaxFileListBytes, MaxFilePaths)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	items := make([]transfer.Item, 0, len(entries))
	for _, raw := range entries {
		items = append(items, transfer.NewItem(transfer.Bytes(transfer.FileList, raw)))
	}
	return transfer.New(items...).Files()
}
