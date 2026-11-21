package radio

import "testing"

// The post-retune skip must be an even number of bytes for every allowed
// sample rate, or I and Q swap for the rest of the stream.
func TestRetuneSkipKeepsIQAlignment(t *testing.T) {
	for _, sr := range []uint32{250_000, 1_024_000, 1_400_000, 1_800_000, 2_048_000, 2_083_334, 2_400_000, 2_400_001} {
		r := New(Config{CenterHz: 100_000_000, SampleRate: sr, FFTSize: 256, FPS: 20, MinDB: -90, MaxDB: -20}, nopSink{}, nil)
		if _, err := r.Tune(TuneRequest{FreqHz: u32p(101_000_000)}); err != nil {
			t.Fatal(err)
		}
		if r.skip%2 != 0 {
			t.Errorf("rate %d: skip %d bytes is odd", sr, r.skip)
		}
	}
}

type nopSink struct{}

func (nopSink) Meta(Meta)    {}
func (nopSink) Frame([]byte) {}

func u32p(v uint32) *uint32 { return &v }
