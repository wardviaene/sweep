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
	// Antenna is an antenna id from the band catalog (nil = unknown).
	Antenna *string `json:"antenna,omitempty"`
	// ScannerLockouts are the scanner's locked-out channels (Hz) per band id
	// ("custom" for a range without a band).
	ScannerLockouts map[string][]uint32 `json:"scanner_lockouts,omitempty"`
	// Autopilot configures the satellite pass autopilot (docs/autopilot.md).
	Autopilot *Autopilot `json:"autopilot,omitempty"`
	// Transcribe configures speech transcription (docs/transcription.md).
	Transcribe *Transcribe `json:"transcribe,omitempty"`
}

// Autopilot holds the pass autopilot settings. Unset fields have defaults:
// min_elevation 20 degrees and listen on.
type Autopilot struct {
	Enabled      bool     `json:"enabled"`
	MinElevation *float64 `json:"min_elevation,omitempty"`
	// Satellites are NORAD ids; empty means all.
	Satellites []int `json:"satellites,omitempty"`
	// Preempt lets the autopilot stop a running survey, scanner or decoder.
	Preempt  bool  `json:"preempt"`
	Listen   *bool `json:"listen,omitempty"`
	RecordIQ bool  `json:"record_iq"`
}

// MinEl is the minimum maximum-elevation of a pass in degrees (default 20).
func (a Autopilot) MinEl() float64 {
	if a.MinElevation == nil {
		return 20
	}
	return *a.MinElevation
}

// ListenOn reports whether the VFO follows the downlink (default true).
func (a Autopilot) ListenOn() bool { return a.Listen == nil || *a.Listen }

// Wants reports whether norad is selected (an empty list selects all).
func (a Autopilot) Wants(norad int) bool {
	if len(a.Satellites) == 0 {
		return true
	}
	for _, n := range a.Satellites {
		if n == norad {
			return true
		}
	}
	return false
}

// AutopilotSettings returns the autopilot settings with nothing nil.
func (c Config) AutopilotSettings() Autopilot {
	var a Autopilot
	if c.Autopilot != nil {
		a = *c.Autopilot
	}
	m, l := a.MinEl(), a.ListenOn()
	a.MinElevation, a.Listen = &m, &l
	if a.Satellites == nil {
		a.Satellites = []int{}
	}
	return a
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
	if c.Antenna != nil {
		a := *c.Antenna
		c.Antenna = &a
	}
	if c.ScannerLockouts != nil {
		m := make(map[string][]uint32, len(c.ScannerLockouts))
		for k, v := range c.ScannerLockouts {
			m[k] = append([]uint32(nil), v...)
		}
		c.ScannerLockouts = m
	}
	if c.Autopilot != nil {
		a := *c.Autopilot
		a.Satellites = append([]int(nil), a.Satellites...)
		if a.MinElevation != nil {
			v := *a.MinElevation
			a.MinElevation = &v
		}
		if a.Listen != nil {
			v := *a.Listen
			a.Listen = &v
		}
		c.Autopilot = &a
	}
	c.Transcribe = c.Transcribe.clone()
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
	if a := c.Autopilot; a != nil {
		if a.MinElevation != nil && (*a.MinElevation < 0 || *a.MinElevation > 90) {
			return errors.New("autopilot.min_elevation must be 0..90")
		}
		for _, n := range a.Satellites {
			if n <= 0 {
				return errors.New("autopilot.satellites: NORAD ids must be positive")
			}
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
