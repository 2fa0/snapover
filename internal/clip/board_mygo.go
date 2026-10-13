package clip

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/2fa0/snapover/internal/crashlog"
)

// MyGoBoard reads and writes the system clipboard through MyGo.
// Call it only after App.Run has started; other goroutines may call it.
type MyGoBoard struct{}

var writeClipboardText = mygo.Clipboard.Write

func (MyGoBoard) Sequence() (uint64, error) { return platformSequence() }

func (MyGoBoard) Snapshot() (Item, error) {
	crashlog.Breadcrumb("开始读取剪贴板")
	raw := mygo.Clipboard.AvailableFormats()
	portable := make([]string, 0, 4)
	for _, f := range mygo.Clipboard.Formats() {
		portable = append(portable, string(f))
	}
	all := make([]string, 0, len(raw)+len(portable))
	all = append(all, raw...)
	all = append(all, portable...)
	crashlog.Breadcrumb("剪贴板格式 " + formatList(all))

	if sens, why := Sensitive(all, readFormat); sens {
		return Item{Sensitive: true, Skip: why}, nil
	}
	if HasImage(all) {
		png := mygo.Clipboard.ReadImage()
		if len(png) == 0 {
			return Item{Skip: "剪贴板有图片格式，但没有读出 PNG"}, nil
		}
		item, err := FromPNG(png)
		if err != nil {
			return Item{Skip: err.Error()}, nil
		}
		return item, nil
	}
	if HasText(all) {
		text := mygo.Clipboard.ReadText()
		if text == "" {
			return Item{}, nil
		}
		item, err := FromText(text)
		if err != nil {
			return Item{Skip: err.Error()}, nil
		}
		return item, nil
	}
	return Item{}, nil
}

func (MyGoBoard) Write(item Item) error {
	crashlog.Breadcrumb(fmt.Sprintf("写入剪贴板 kind=%s bytes=%d", item.Kind, item.Size))
	switch item.Kind {
	case KindText:
		return writeClipboardText(transfer.TextData(clipboardText(item.Text, runtime.GOOS)))
	case KindImage:
		return mygo.Clipboard.WriteImage(item.PNG)
	default:
		return errEmptyWrite
	}
}

func formatList(all []string) string {
	if len(all) == 0 {
		return "(无)"
	}
	return strings.Join(all, ",")
}

func readFormat(name string) ([]byte, bool) {
	buf, err := mygo.Clipboard.ReadFormat(transfer.Format(name))
	if err != nil || len(buf) == 0 {
		return nil, false
	}
	return buf, true
}
