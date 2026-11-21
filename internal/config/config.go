// Package config stores local, per-machine settings (the receiver location)
// outside the repository, in ~/.config/sweep/config.json by default.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Receiver is the antenna location, used as the reference point for ADS-B.
type Receiver struct {
	Lat  float64  `json:"lat"`
	Lon  float64  `json:"lon"`
	AltM *float64 `json:"alt_m,omitempty"`
}

// Config is the stored configuration.
type Config struct {
	Receiver *Receiver `json:"receiver"`
	// MeterIDs are the utility meters the user owns. The meters decoder
	// shows only these; every other meter is counted but never published.
	MeterIDs []string `json:"meter_ids,omitempty"`
}

// Store loads and saves the config file.
type Store struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

// DefaultPath is ~/.config/sweep/config.json.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "sweep-config.json"
	}
	return filepath.Join(home, ".config", "sweep", "config.json")
}

// Open reads path if it exists.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Get returns a copy of the config.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	if c.Receiver != nil {
		r := *c.Receiver
		c.Receiver = &r
	}
	c.MeterIDs = append([]string(nil), c.MeterIDs...)
	return c
}

// Validate checks ranges.
func (c Config) Validate() error {
	if r := c.Receiver; r != nil {
		if r.Lat < -90 || r.Lat > 90 {
			return errors.New("receiver.lat must be -90..90")
		}
		if r.Lon < -180 || r.Lon > 180 {
			return errors.New("receiver.lon must be -180..180")
		}
	}
	return nil
}

// Set validates, stores and writes the config (file mode 0600).
func (s *Store) Set(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.cfg = c
	return nil
}
