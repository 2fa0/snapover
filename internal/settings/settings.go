package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const DefaultMaxImageMB = 20

const DefaultMenuBarIcon = 2

const (
	DefaultMaxFileMB       = 200
	MaxFileMB        int64 = (1<<63 - 1) >> 20
)

const DefaultHistoryMB = 2048

const MaxHistoryMB = 1 << 20

const (
	DefaultMaxTextMB = 1
	MaxTextMB        = 20
)

// The quick history window keeps the flyout defaults until the user drags it
// to another size. The bounds keep it usable: it must still show a few rows,
// and it cannot grow beyond what the work area can hold.
const (
	DefaultPickerWidth  = 380
	DefaultPickerHeight = 440
	MinPickerWidth      = 280
	MaxPickerWidth      = 1200
	MinPickerHeight     = 200
	MaxPickerHeight     = 1000
)

// Values is the small set of options the settings page can change.
// MenuBarIcon keeps its original key and icon IDs for both platforms.
type Values struct {
	Paused         bool   `json:"paused"`
	TextOnly       bool   `json:"text_only"`
	MaxImageMB     int    `json:"max_image_mb"`
	MaxTextMB      int    `json:"max_text_mb"`
	MaxFileMB      int64  `json:"max_file_mb"`
	OpenAtLogin    bool   `json:"open_at_login"`
	MenuBarIcon    int    `json:"menu_bar_icon,omitempty"`
	KeepAwake      bool   `json:"keep_awake,omitempty"`
	ReceiveDir     string `json:"receive_dir,omitempty"`
	DisableSendTo  bool   `json:"disable_send_to,omitempty"`
	PickerShortcut string `json:"picker_shortcut,omitempty"`
	PickerWidth    int    `json:"picker_width,omitempty"`
	PickerHeight   int    `json:"picker_height,omitempty"`
	CopyShortcut   string `json:"copy_shortcut,omitempty"`
	ManualSync     bool   `json:"manual_sync,omitempty"`
	AllowTailscale bool   `json:"allow_tailscale,omitempty"`
	HistoryMaxMB   int    `json:"history_max_mb"`

	WindowsNetworkGuideDismissed bool `json:"windows_network_guide_dismissed,omitempty"`
	MacNetworkGuideDismissed     bool `json:"mac_network_guide_dismissed,omitempty"`

	// DiscoveryMode is empty when every physical segment is used, including
	// segments that appear later. "selected" limits discovery to DiscoveryNets.
	// DiscoverySeen records segments present at the last explicit edit, so a
	// newly attached segment stays unchecked and can be called out.
	DiscoveryMode string   `json:"discovery_mode,omitempty"`
	DiscoveryNets []string `json:"discovery_nets,omitempty"`
	DiscoverySeen []string `json:"discovery_seen,omitempty"`
}

// File is settings.json in the data directory.
type File struct {
	Path   string
	Values Values
}

func Load(dataDir string) (*File, error) {
	f := &File{
		Path:   filepath.Join(dataDir, "settings.json"),
		Values: Values{MaxImageMB: DefaultMaxImageMB, MaxTextMB: DefaultMaxTextMB, MaxFileMB: DefaultMaxFileMB, HistoryMaxMB: DefaultHistoryMB, MenuBarIcon: DefaultMenuBarIcon},
	}
	buf, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(buf, &f.Values); err != nil {
		return nil, err
	}
	f.Values.MaxImageMB = clampMB(f.Values.MaxImageMB)
	f.Values.MaxTextMB = clampTextMB(f.Values.MaxTextMB)
	f.Values.MaxFileMB = clampFileMB(f.Values.MaxFileMB)
	f.Values.MenuBarIcon = clampMenuBarIcon(f.Values.MenuBarIcon)
	f.Values.PickerWidth, f.Values.PickerHeight = clampPickerSize(f.Values.PickerWidth, f.Values.PickerHeight)
	if f.Values.HistoryMaxMB <= 0 {
		f.Values.HistoryMaxMB = DefaultHistoryMB
	}
	if f.Values.HistoryMaxMB > MaxHistoryMB {
		f.Values.HistoryMaxMB = MaxHistoryMB
	}
	return f, nil
}

func (f *File) Save() error {
	if f.Values.HistoryMaxMB <= 0 {
		f.Values.HistoryMaxMB = DefaultHistoryMB
	}
	if f.Values.HistoryMaxMB > MaxHistoryMB {
		f.Values.HistoryMaxMB = MaxHistoryMB
	}
	f.Values.MaxImageMB = clampMB(f.Values.MaxImageMB)
	f.Values.MaxTextMB = clampTextMB(f.Values.MaxTextMB)
	f.Values.MaxFileMB = clampFileMB(f.Values.MaxFileMB)
	f.Values.MenuBarIcon = clampMenuBarIcon(f.Values.MenuBarIcon)
	f.Values.PickerWidth, f.Values.PickerHeight = clampPickerSize(f.Values.PickerWidth, f.Values.PickerHeight)
	buf, err := json.MarshalIndent(f.Values, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

func clampFileMB(n int64) int64 {
	if n < 1 {
		return DefaultMaxFileMB
	}
	return min(n, MaxFileMB)
}

func clampMenuBarIcon(n int) int {
	if n < 1 || n > 3 {
		return DefaultMenuBarIcon
	}
	return n
}

func clampTextMB(n int) int {
	if n < 1 {
		return DefaultMaxTextMB
	}
	return min(n, MaxTextMB)
}

// PickerSize returns the stored quick history size, clamped to the supported
// range so a hand-edited settings file cannot open an unusable window.
func (f *File) PickerSize() (int, int) {
	return clampPickerSize(f.Values.PickerWidth, f.Values.PickerHeight)
}

func clampPickerSize(width, height int) (int, int) {
	if width < MinPickerWidth {
		width = DefaultPickerWidth
	}
	if width > MaxPickerWidth {
		width = MaxPickerWidth
	}
	if height < MinPickerHeight {
		height = DefaultPickerHeight
	}
	if height > MaxPickerHeight {
		height = MaxPickerHeight
	}
	return width, height
}

func clampMB(n int) int {
	if n < 1 {
		return DefaultMaxImageMB
	}
	if n > DefaultMaxImageMB {
		return DefaultMaxImageMB
	}
	return n
}
