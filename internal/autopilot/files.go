package autopilot

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

const (
	historyLen = 50
	passFile   = "pass.json"
)

// Entry is one finished (or running) pass: the history item and the content
// of pass.json.
type Entry struct {
	Norad      int     `json:"norad"`
	Name       string  `json:"name"`
	AOS        int64   `json:"aos"`
	LOS        int64   `json:"los"`
	MaxElev    float64 `json:"max_elevation_deg"`
	DownlinkHz int64   `json:"downlink_hz"`
	Decoder    string  `json:"decoder"`
	Baud       int     `json:"baud,omitempty"`
	Packets    int     `json:"packets"`
	Images     int     `json:"images"`
	Audio      string  `json:"audio"`
	IQ         string  `json:"iq,omitempty"`
	Dir        string  `json:"dir"`
	State      string  `json:"state"` // running | done | stopped | error
	Reason     string  `json:"reason"`
	Started    float64 `json:"started,omitempty"`
	Ended      float64 `json:"ended,omitempty"`
	Manual     bool    `json:"manual,omitempty"`
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// dirName is <YYYYmmdd-HHMMSS>_<norad>_<name> (UTC).
func dirName(t time.Time, norad int, name string) string {
	n := unsafeName.ReplaceAllString(name, "_")
	if n == "" {
		n = "sat"
	}
	return fmt.Sprintf("%s_%d_%s", t.UTC().Format("20060102-150405"), norad, n)
}

// makePassDir creates a fresh pass directory under root and returns its name.
func makePassDir(root string, t time.Time, norad int, name string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	base := dirName(t, norad, name)
	for i := 0; i < 100; i++ {
		d := base
		if i > 0 {
			d = fmt.Sprintf("%s-%d", base, i+1)
		}
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err == nil {
			return d, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot create a pass directory in %s", root)
}

// writePassJSON writes pass.json atomically.
func writePassJSON(root string, e Entry) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(root, e.Dir, passFile)
	if err := os.WriteFile(p+".tmp", append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// loadHistory reads the pass directories, newest first, at most n. A pass
// still marked running was cut off by a restart.
func loadHistory(root string, n int) []Entry {
	ents, _ := os.ReadDir(root)
	out := []Entry{}
	for _, d := range ents {
		if !d.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, d.Name(), passFile))
		if err != nil {
			continue
		}
		var e Entry
		if json.Unmarshal(b, &e) != nil {
			continue
		}
		e.Dir = d.Name()
		if e.State == "running" {
			e.State, e.Reason = "stopped", "interrupted"
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AOS != out[j].AOS {
			return out[i].AOS > out[j].AOS
		}
		return out[i].Dir > out[j].Dir
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// wavFile writes 16-bit mono PCM and fixes the header on close.
type wavFile struct {
	f    *os.File
	rate int
	n    int64
}

func newWAV(path string, rate int) (*wavFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &wavFile{f: f, rate: rate}
	if err := w.header(); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(44, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *wavFile) header() error {
	var h [44]byte
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+w.n))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 1) // mono
	binary.LittleEndian.PutUint32(h[24:], uint32(w.rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(w.rate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(w.n))
	_, err := w.f.WriteAt(h[:], 0)
	return err
}

func (w *wavFile) write(pcm []int16) error {
	b := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	n, err := w.f.Write(b)
	w.n += int64(n)
	return err
}

func (w *wavFile) close() error {
	err := w.header()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// copyFile copies src to dst (best effort helper for SSTV images).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
