package clip

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/2fa0/snapover/internal/crashlog"
	"github.com/oklog/ulid/v2"
)

// MyGoBoard reads and writes the system clipboard through MyGo.
// Call it only after App.Run has started; other goroutines may call it.
type MyGoBoard struct {
	TextLimit func() int
}

var (
	writeClipboardData    = mygo.Clipboard.Write
	flushClipboardData    = mygo.Clipboard.Flush
	runOnClipboardThread  = mygo.RunOnMain
	readClipboardData     = mygo.Clipboard.ReadFormat
	prepareClipboardImage = prepareNativeImage
	clipboardSequence     = platformSequence
	remoteMarker          = []byte(ulid.Make().String())
)

const remoteFormat transfer.Format = "application/x-snapover-remote"
const localFormat transfer.Format = "application/x-snapover-local"

// PreparedWrite owns temporary native buffers until publication or cancellation.
type PreparedWrite struct {
	Publish func() error
	release func()
}

func (p PreparedWrite) Close() {
	if p.release != nil {
		p.release()
	}
}

func (MyGoBoard) Sequence() (uint64, error) { return platformSequence() }

// OwnsSequence checks a tiny representation, never the image. The second
// sequence read prevents a new external copy from inheriting an old marker.
func (MyGoBoard) OwnsSequence(seq uint64) bool {
	marker, err := readClipboardData(remoteFormat)
	if err != nil || strings.TrimRight(string(marker), "\x00") != string(remoteMarker) {
		return false
	}
	after, err := clipboardSequence()
	return err == nil && after == seq
}

func (b MyGoBoard) Snapshot() (result Item, resultErr error) {
	defer crashlog.TrackOperation("读取剪贴板")()
	crashlog.Breadcrumb("开始读取剪贴板")
	raw := mygo.Clipboard.AvailableFormats()
	portable := make([]string, 0, 4)
	for _, f := range mygo.Clipboard.Formats() {
		portable = append(portable, string(f))
	}
	all := make([]string, 0, len(raw)+len(portable))
	all = append(all, raw...)
	all = append(all, portable...)
	for _, format := range all {
		if format == string(localFormat) {
			marker, err := readClipboardData(localFormat)
			if err == nil && strings.TrimRight(string(marker), "\x00") == string(remoteMarker) {
				// Watch checks the sequence again after Snapshot, so another
				// process's new copy cannot inherit this classification.
				defer func() { result.LocalWrite = true }()
			}
			break
		}
	}
	crashlog.Breadcrumb("剪贴板格式 " + formatList(all))

	if sens, why := Sensitive(all, readFormat); sens {
		return Item{Sensitive: true, Skip: why}, nil
	}
	if HasFiles(all) {
		paths, err := readClipboardFiles()
		if err != nil {
			if errors.Is(err, ErrClipboardBusy) {
				return Item{}, err
			}
			return Item{Skip: "无法读取文件剪贴板"}, nil
		}
		item, err := FromFiles(paths)
		if err != nil {
			return Item{Skip: err.Error()}, nil
		}
		return item, nil
	}
	if HasImage(all) {
		item, err := readClipboardImage()
		if err != nil {
			if errors.Is(err, ErrClipboardBusy) {
				return Item{}, err
			}
			return Item{Skip: err.Error()}, nil
		}
		return item, nil
	}
	if HasText(all) {
		limit := DefaultMaxTextBytes
		if b.TextLimit != nil {
			limit = b.TextLimit()
		}
		text, err := readLimitedClipboardText(limit)
		if err != nil {
			if errors.Is(err, ErrClipboardBusy) {
				return Item{}, err
			}
			return Item{Skip: err.Error()}, nil
		}
		if text == "" {
			return Item{}, nil
		}
		item, err := FromTextWithLimit(text, limit)
		if err != nil {
			return Item{Skip: err.Error()}, nil
		}
		return item, nil
	}
	return Item{}, nil
}

func (b MyGoBoard) Write(item Item) error {
	publish, err := b.PrepareWrite(item)
	if err != nil {
		return err
	}
	defer publish.Close()
	return publish.Publish()
}

// PrepareWrite performs image conversion on the calling worker. Only the
// returned publication callback requires the native clipboard thread.
func (b MyGoBoard) PrepareWrite(item Item) (PreparedWrite, error) {
	return b.prepareWrite(item, false)
}

func (b MyGoBoard) PrepareRemoteWrite(item Item) (PreparedWrite, error) {
	return b.prepareWrite(item, true)
}

func (MyGoBoard) prepareWrite(item Item, remote bool) (PreparedWrite, error) {
	if item.Kind == KindImage {
		if publish, handled, err := prepareClipboardImage(item, remote); handled {
			return publish, err
		}
	}
	if item.Kind == KindFiles {
		data, err := transfer.FileData(item.Files...)
		if err != nil {
			return PreparedWrite{}, err
		}
		if len(item.Files) == 0 {
			return PreparedWrite{}, errEmptyWrite
		}
		if remote {
			var items []transfer.Item
			for _, file := range data.Items() {
				var reps []transfer.Representation
				for _, format := range file.Formats() {
					raw, err := file.Read(format)
					if err != nil {
						return PreparedWrite{}, err
					}
					reps = append(reps, transfer.Bytes(format, raw))
				}
				reps = append(reps, transfer.Bytes(remoteFormat, remoteMarker))
				items = append(items, transfer.NewItem(reps...))
			}
			data = transfer.New(items...)
		}
		return PreparedWrite{Publish: func() error { return publishClipboard(data, item.Kind, item.Size) }}, nil
	}
	var reps []transfer.Representation
	switch item.Kind {
	case KindText:
		reps = append(reps, transfer.Bytes(transfer.Text, []byte(clipboardText(item.Text, runtime.GOOS))))
	case KindImage:
		reps = append(reps, transfer.Bytes(transfer.PNG, item.PNG))
	default:
		return PreparedWrite{}, errEmptyWrite
	}
	if remote {
		reps = append(reps, transfer.Bytes(remoteFormat, remoteMarker))
	} else {
		reps = append(reps, transfer.Bytes(localFormat, remoteMarker))
	}
	data := transfer.New(transfer.NewItem(reps...))
	return PreparedWrite{Publish: func() error { return publishClipboard(data, item.Kind, item.Size) }}, nil
}

func publishClipboard(data transfer.Data, kind Kind, size int) error {
	defer crashlog.TrackOperation("写入并持久化剪贴板")()
	crashlog.Breadcrumb(fmt.Sprintf("写入剪贴板 kind=%s bytes=%d", kind, size))
	// Publish and flush in one main-thread callback so another queued write
	// cannot replace our data between the two operations. Native storage then
	// serves later pastes without calling back into Snapover's providers.
	err := errors.New("剪贴板线程已停止")
	runOnClipboardThread(func() {
		err = writeClipboardData(data)
		if err == nil {
			if err = flushClipboardData(); err != nil {
				err = fmt.Errorf("持久化剪贴板失败：%w", err)
			}
		}
	})
	return err
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
