package source

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// File plays back a cu8 recording in real time, looping.
type File struct {
	mu      sync.Mutex
	f       *os.File
	rate    float64
	start   time.Time
	emitted int64
}

// OpenFile opens a cu8 recording for playback at sampleRate.
func OpenFile(path string, sampleRate uint32) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &File{f: f, rate: float64(sampleRate)}, nil
}

var recName = regexp.MustCompile(`_(\d+)Hz_(\d+)sps\.cu8$`)

// ParseRecordingName extracts center frequency and sample rate from a name
// like 20261121-125000_433920000Hz_1024000sps.cu8.
func ParseRecordingName(path string) (freqHz, rate uint32, err error) {
	m := recName.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return 0, 0, fmt.Errorf("%s: name must end in _<freq>Hz_<rate>sps.cu8 (or pass -iq-freq and -iq-rate)", path)
	}
	f, _ := strconv.ParseUint(m[1], 10, 32)
	r, _ := strconv.ParseUint(m[2], 10, 32)
	return uint32(f), uint32(r), nil
}

// RecordingName builds a file name that ParseRecordingName understands.
func RecordingName(t time.Time, freqHz, rate uint32) string {
	return fmt.Sprintf("%s_%dHz_%dsps.cu8", t.Format("20060102-150405"), freqHz, rate)
}

// Read returns IQ bytes paced to the sample rate, looping at end of file.
func (f *File) Read(p []byte) (int, error) {
	n := len(p) &^ 1
	if n > 32768 {
		n = 32768
	}
	f.mu.Lock()
	if f.start.IsZero() {
		f.start = time.Now()
	}
	due := f.start.Add(time.Duration(float64(f.emitted) / f.rate * float64(time.Second)))
	f.mu.Unlock()
	if d := time.Until(due); d > 0 {
		time.Sleep(d)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	got, err := io.ReadFull(f.f, p[:n])
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		if _, err := f.f.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		if got == 0 {
			got, err = io.ReadFull(f.f, p[:n])
			if err != nil && err != io.ErrUnexpectedEOF {
				return 0, fmt.Errorf("recording is empty")
			}
		}
	} else if err != nil {
		return 0, err
	}
	got &^= 1
	f.emitted += int64(got / 2)
	return got, nil
}

// SetFreq is rejected: a recording has a fixed center.
func (f *File) SetFreq(uint32) error { return fmt.Errorf("playing a recording: tuning is disabled") }

// SetSampleRate is rejected.
func (f *File) SetSampleRate(uint32) error {
	return fmt.Errorf("playing a recording: sample rate is fixed")
}

// SetGain is a no-op.
func (f *File) SetGain(*float64) error { return nil }

// Close closes the file.
func (f *File) Close() error { return f.f.Close() }
