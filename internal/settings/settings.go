package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const DefaultMaxImageMB = 20

// Values is the small set of options the settings page can change.
type Values struct {
	Paused      bool `json:"paused"`
	TextOnly    bool `json:"text_only"`
	MaxImageMB  int  `json:"max_image_mb"`
	OpenAtLogin bool `json:"open_at_login"`
}

// File is settings.json in the data directory.
type File struct {
	Path   string
	Values Values
}

func Load(dataDir string) (*File, error) {
	f := &File{
		Path:   filepath.Join(dataDir, "settings.json"),
		Values: Values{MaxImageMB: DefaultMaxImageMB},
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
	return f, nil
}

func (f *File) Save() error {
	f.Values.MaxImageMB = clampMB(f.Values.MaxImageMB)
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

func clampMB(n int) int {
	if n < 1 {
		return DefaultMaxImageMB
	}
	if n > DefaultMaxImageMB {
		return DefaultMaxImageMB
	}
	return n
}
