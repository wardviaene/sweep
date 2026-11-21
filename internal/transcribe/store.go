package transcribe

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Retention: the newest MaxClips clips or MaxAge, whichever is smaller.
const (
	MaxClips = 500
	MaxAge   = 7 * 24 * time.Hour
)

// Entry is one transcribed clip (the transcribe event and /api/transcripts item).
type Entry struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"` // scanner | survey | manual
	FreqHz    uint32  `json:"freq_hz"`
	Label     string  `json:"label"`
	Mode      string  `json:"mode"`
	Start     float64 `json:"start"`
	DurationS float64 `json:"duration_s"`
	Text      string  `json:"text"`
	Empty     bool    `json:"empty"`
	Model     string  `json:"model"`
	LatencyS  float64 `json:"latency_s"`
	URL       string  `json:"url"`
}

// Store keeps the entries in memory and in <dir>/index.jsonl.
type Store struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	entries []Entry // oldest first
}

// OpenStore loads dir/index.jsonl, drops entries whose clip is gone or that
// the retention rule expires (deleting their clips) and rewrites the index.
func OpenStore(dir string) (*Store, error) {
	s := &Store{dir: dir, now: time.Now}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, "index.jsonl"))
	if err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<16), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.ID == "" {
				continue
			}
			if _, err := os.Stat(s.clipPath(e.ID)); err != nil {
				continue
			}
			s.entries = append(s.entries, e)
		}
		f.Close()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	sort.SliceStable(s.entries, func(i, j int) bool { return s.entries[i].Start < s.entries[j].Start })
	s.prune()
	s.rewrite()
	return s, nil
}

// ClipPath is where the clip with this id lives. The id is
// <YYYYmmdd>-<HHMMSS>_<freq>[-n].
func ClipPath(dir, id string) string {
	date, name, _ := strings.Cut(id, "-")
	return filepath.Join(dir, date, name+".wav")
}

func (s *Store) clipPath(id string) string { return ClipPath(s.dir, id) }

// URLFor is the file route of a clip id.
func URLFor(id string) string {
	date, name, _ := strings.Cut(id, "-")
	return "/api/files/clips/" + date + "/" + name + ".wav"
}

// prune applies the retention rule (callers hold no lock or own s.mu).
func (s *Store) prune() bool {
	cutoff := float64(s.now().Add(-MaxAge).UnixMilli()) / 1e3
	var keep []Entry
	for _, e := range s.entries {
		if e.Start >= cutoff {
			keep = append(keep, e)
		} else {
			s.remove(e)
		}
	}
	if len(keep) > MaxClips {
		for _, e := range keep[:len(keep)-MaxClips] {
			s.remove(e)
		}
		keep = keep[len(keep)-MaxClips:]
	}
	changed := len(keep) != len(s.entries)
	s.entries = keep
	return changed
}

func (s *Store) remove(e Entry) {
	p := s.clipPath(e.ID)
	_ = os.Remove(p)
	_ = os.Remove(filepath.Dir(p)) // only succeeds when the day is empty
}

func (s *Store) rewrite() {
	tmp := filepath.Join(s.dir, "index.jsonl.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for _, e := range s.entries {
		b, _ := json.Marshal(e)
		w.Write(b)
		w.WriteByte('\n')
	}
	err = w.Flush()
	f.Close()
	if err == nil {
		_ = os.Rename(tmp, filepath.Join(s.dir, "index.jsonl"))
	}
}

// Add stores an entry (its clip is already written) and applies retention.
func (s *Store) Add(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	if s.prune() {
		s.rewrite()
		return
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
	f.Close()
}

// Query returns entries newer than since (start time, unix seconds), newest
// first, optionally filtered by a case-insensitive text match and a frequency.
func (s *Store) Query(since float64, limit int, q string, freqHz uint32) []Entry {
	q = strings.ToLower(strings.TrimSpace(q))
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Entry{}
	for i := len(s.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := s.entries[i]
		if e.Start <= since || (freqHz != 0 && e.FreqHz != freqHz) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Text+" "+e.Label), q) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Len is the number of stored entries.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
