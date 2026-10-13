package sync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrFingerprintMismatch means a known device presented a different certificate.
var ErrFingerprintMismatch = errors.New("证书指纹不匹配")

// Peer is a device this machine has trusted on first connect.
type Peer struct {
	Name        string    `json:"name"`
	Platform    string    `json:"platform"`
	Fingerprint string    `json:"fingerprint"`
	Addr        string    `json:"addr,omitempty"`
	TrustedAt   time.Time `json:"trusted_at"`
}

// Store persists peers approved by the pairing flow.
// A later certificate change is refused until the record is deleted.
type Store struct {
	path string
	mu   sync.Mutex
	Peer map[string]Peer `json:"peers"`
}

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dataDir, "peers.json"), Peer: map[string]Peer{}}
	buf, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytesTrim(buf)) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(buf, s); err != nil {
		return nil, err
	}
	if s.Peer == nil {
		s.Peer = map[string]Peer{}
	}
	return s, nil
}

func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.Peer
	s.Peer = map[string]Peer{}
	if err := s.saveLocked(); err != nil {
		s.Peer = old
		return err
	}
	return nil
}

// Device is a trusted peer plus its id.
type Device struct {
	ID   string
	Peer Peer
}

// List returns trusted devices, newest trust first.
func (s *Store) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.Peer))
	for id, peer := range s.Peer {
		out = append(out, Device{ID: id, Peer: peer})
	}
	sortDevices(out)
	return out
}

// Forget drops one trusted device. The live connection, if any, is left to the caller.
func (s *Store) Forget(id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.Peer[id]
	delete(s.Peer, id)
	if err := s.saveLocked(); err != nil {
		if exists {
			s.Peer[id] = old
		}
		return err
	}
	return nil
}

// Check verifies a known fingerprint without creating or updating trust.
func (s *Store) Check(id, fingerprint string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peer, ok := s.Peer[strings.ToLower(strings.TrimSpace(id))]
	if ok && normalizeFP(peer.Fingerprint) != normalizeFP(fingerprint) {
		return false, ErrFingerprintMismatch
	}
	return ok, nil
}

func sortDevices(list []Device) {
	for i := 1; i < len(list); i++ {
		item := list[i]
		j := i
		for j > 0 && deviceLess(item, list[j-1]) {
			list[j] = list[j-1]
			j--
		}
		list[j] = item
	}
}

func deviceLess(a, b Device) bool {
	if !a.Peer.TrustedAt.Equal(b.Peer.TrustedAt) {
		return a.Peer.TrustedAt.After(b.Peer.TrustedAt)
	}
	return a.ID < b.ID
}

// Allow trusts id on first sight and refuses a changed fingerprint.
// first is true when this call created the record.
func (s *Store) Allow(id, fingerprint, name, platform, addr string) (bool, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	fp := normalizeFP(fingerprint)
	if id == "" || fp == "" {
		return false, errors.New("缺少设备 id 或指纹")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.Peer[id]; ok {
		if normalizeFP(cur.Fingerprint) != fp {
			return false, ErrFingerprintMismatch
		}
		old := cur
		if name != "" {
			cur.Name = name
		}
		if platform != "" {
			cur.Platform = platform
		}
		if addr != "" {
			cur.Addr = addr
		}
		s.Peer[id] = cur
		if err := s.saveLocked(); err != nil {
			s.Peer[id] = old
			return false, err
		}
		return false, nil
	}
	s.Peer[id] = Peer{
		Name:        name,
		Platform:    platform,
		Fingerprint: fp,
		Addr:        addr,
		TrustedAt:   time.Now(),
	}
	if err := s.saveLocked(); err != nil {
		delete(s.Peer, id)
		return false, err
	}
	return true, nil
}

func (s *Store) saveLocked() error {
	buf, err := json.MarshalIndent(struct {
		Peers map[string]Peer `json:"peers"`
	}{Peers: s.Peer}, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func normalizeFP(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ":", "")
	return s
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
