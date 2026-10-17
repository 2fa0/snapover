package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const DefaultMaxImageMB = 20

const DefaultHistoryMB = 2048

const MaxHistoryMB = 1 << 20

const (
	DefaultMaxTextMB = 1
	MaxTextMB        = 20
)

// Values is the small set of options the settings page can change.
type Values struct {
	Paused         bool   `json:"paused"`
	TextOnly       bool   `json:"text_only"`
	MaxImageMB     int    `json:"max_image_mb"`
	MaxTextMB      int    `json:"max_text_mb"`
	OpenAtLogin    bool   `json:"open_at_login"`
	ReceiveDir     string `json:"receive_dir,omitempty"`
	DisableSendTo  bool   `json:"disable_send_to,omitempty"`
	PickerShortcut string `json:"picker_shortcut,omitempty"`
	CopyShortcut   string `json:"copy_shortcut,omitempty"`
	ManualSync     bool   `json:"manual_sync,omitempty"`
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
		Values: Values{MaxImageMB: DefaultMaxImageMB, MaxTextMB: DefaultMaxTextMB, HistoryMaxMB: DefaultHistoryMB},
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

func clampTextMB(n int) int {
	if n < 1 {
		return DefaultMaxTextMB
	}
	if n > MaxTextMB {
		return MaxTextMB
	}
	return n
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
