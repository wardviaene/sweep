package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	s, err := Open(p)
	if err != nil || s.Get().Receiver != nil {
		t.Fatalf("open empty: %v %+v", err, s.Get())
	}
	if err := s.Set(Config{Receiver: &Receiver{Lat: 95}}); err == nil {
		t.Fatal("lat 95 accepted")
	}
	if err := s.Set(Config{Receiver: &Receiver{Lat: 12.34, Lon: 56.78}}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	s2, err := Open(p)
	if err != nil || s2.Get().Receiver == nil || s2.Get().Receiver.Lon != 56.78 {
		t.Fatalf("reload: %v %+v", err, s2.Get())
	}
}
