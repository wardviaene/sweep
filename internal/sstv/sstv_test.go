package sstv

import (
	"image"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// channel simulates the path from the encoder to the decoder: a sample-clock
// offset (the audio was generated at rate*(1+ppm)), white noise at the given
// SNR and leading/trailing noise.
func channel(pcm []int16, snrDB float64, lead, trail int, seed int64) []int16 {
	rng := rand.New(rand.NewSource(seed))
	var p float64
	for _, v := range pcm {
		p += float64(v) * float64(v)
	}
	p /= float64(len(pcm))
	sigma := math.Sqrt(p / math.Pow(10, snrDB/10))
	out := make([]int16, 0, len(pcm)+lead+trail)
	noise := func() int16 {
		return int16(math.Max(-32768, math.Min(32767, rng.NormFloat64()*sigma)))
	}
	for i := 0; i < lead; i++ {
		out = append(out, noise())
	}
	for _, v := range pcm {
		x := float64(v) + rng.NormFloat64()*sigma
		out = append(out, int16(math.Max(-32768, math.Min(32767, x))))
	}
	for i := 0; i < trail; i++ {
		out = append(out, noise())
	}
	return out
}

type result struct {
	mode  *Mode
	img   *image.RGBA
	rows  int
	lines []int
}

func decodeAll(t *testing.T, pcm []int16, rate int, forced *Mode) result {
	t.Helper()
	var res result
	d := NewDecoder(rate, forced)
	d.OnStart = func(m *Mode) {
		if res.mode == nil {
			res.mode = m
		}
	}
	d.OnLine = func(n int) { res.lines = append(res.lines, n) }
	d.OnImage = func(img *image.RGBA, rows int) {
		if res.img == nil {
			cp := image.NewRGBA(img.Rect)
			copy(cp.Pix, img.Pix)
			res.img, res.rows = cp, rows
		}
	}
	for i := 0; i < len(pcm); i += 1000 {
		d.Feed(pcm[i:min(i+1000, len(pcm))])
	}
	d.Finish()
	return res
}

func luma(r, g, b uint8) float64 { return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b) }

// errors returns the mean absolute luminance and RGB errors.
func errors(a, b *image.RGBA) (y, rgb float64) {
	n := 0
	for i := 0; i < len(a.Pix); i += 4 {
		y += math.Abs(luma(a.Pix[i], a.Pix[i+1], a.Pix[i+2]) - luma(b.Pix[i], b.Pix[i+1], b.Pix[i+2]))
		rgb += (math.Abs(float64(a.Pix[i])-float64(b.Pix[i])) + math.Abs(float64(a.Pix[i+1])-float64(b.Pix[i+1])) + math.Abs(float64(a.Pix[i+2])-float64(b.Pix[i+2]))) / 3
		n++
	}
	return y / float64(n), rgb / float64(n)
}

func TestTimings(t *testing.T) {
	// the published line / pair durations
	want := map[string]float64{"Robot36": 150, "PD90": 703.04, "PD120": 508.48, "PD180": 754.24, "Martin1": 446.446, "Scottie1": 428.22}
	for _, m := range Modes {
		if math.Abs(m.UnitMs-want[m.Name]) > 0.005 {
			t.Errorf("%s: unit %.3f ms, want %.3f", m.Name, m.UnitMs, want[m.Name])
		}
		for _, c := range m.Comps {
			if end := c.OffMs + c.DurMs; end > m.UnitMs+1e-6 {
				t.Errorf("%s: component ends at %.3f after the unit (%.3f)", m.Name, end, m.UnitMs)
			}
		}
	}
	for _, m := range Modes {
		if ByVIS(m.VIS) != m {
			t.Errorf("ByVIS(%d)", m.VIS)
		}
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]string{"PD120": "PD120", "robot 36": "Robot36", "martin-1": "Martin1", "Scottie_1": "Scottie1", "r36": "Robot36"} {
		m, err := ParseMode(in)
		if err != nil || m.Name != want {
			t.Errorf("%q: %v %v", in, m, err)
		}
	}
	if m, err := ParseMode("auto"); m != nil || err != nil {
		t.Error("auto")
	}
	if _, err := ParseMode("pd240"); err == nil {
		t.Error("pd240 accepted")
	}
}

// maxYErr is the maximum mean absolute luminance error (0-255) at 20 dB SNR and
// a 0.5% clock offset (measured: 2.5 to 4.7). PD120 (0.19 ms per pixel, about
// two samples) is the hardest mode.
var maxYErr = map[string]float64{"Robot36": 8, "PD90": 8, "PD120": 8, "PD180": 8, "Martin1": 8, "Scottie1": 8}

func TestRoundTrip(t *testing.T) {
	for _, m := range Modes {
		t.Run(m.Name, func(t *testing.T) {
			if testing.Short() && (m.Name == "PD180" || m.Name == "PD120") {
				t.Skip("slow")
			}
			ref := TestPattern(m.Width, m.Height)
			pcm := Encode(m, ref, Rate*1.005) // decoder sees a 0.5% slower clock
			pcm = channel(pcm, 20, Rate, Rate, 1)
			res := decodeAll(t, pcm, Rate, nil)
			if res.mode != m {
				t.Fatalf("mode %v, want %s", res.mode, m.Name)
			}
			if res.img == nil || res.rows != m.Height {
				t.Fatalf("image %v rows %d, want %d", res.img != nil, res.rows, m.Height)
			}
			y, rgb := errors(ref, res.img)
			t.Logf("%s: mean abs error Y %.2f, RGB %.2f (of 255)", m.Name, y, rgb)
			if y > maxYErr[m.Name] {
				t.Errorf("Y error %.2f > %.2f", y, maxYErr[m.Name])
			}
			if len(res.lines) != m.Units() || res.lines[len(res.lines)-1] != m.Height {
				t.Errorf("OnLine calls %d (last %v)", len(res.lines), res.lines[len(res.lines)-1:])
			}
		})
	}
}

func TestInputRatesAndForcedMode(t *testing.T) {
	m, _ := ParseMode("Robot36")
	ref := TestPattern(m.Width, m.Height)
	for _, rate := range []int{8000, 22050, 32000, 48000} {
		pcm := Encode(m, ref, float64(rate)*0.995)
		pcm = channel(pcm, 25, rate, rate, 2)
		res := decodeAll(t, pcm, rate, nil)
		if res.mode != m || res.img == nil {
			t.Fatalf("rate %d: no picture (mode %v)", rate, res.mode)
		}
		y, _ := errors(ref, res.img)
		t.Logf("rate %d: Y error %.2f", rate, y)
		if y > 12 {
			t.Errorf("rate %d: Y error %.2f", rate, y)
		}
	}
	// forced mode: the VIS header is cut off, the decoder syncs on line pulses
	pcm := Encode(m, ref, Rate)
	pcm = channel(pcm[int(1.0*Rate):], 25, Rate, Rate, 3)
	res := decodeAll(t, pcm, Rate, m)
	if res.img == nil || res.mode != m {
		t.Fatalf("forced: no picture")
	}
	if y, _ := errors(ref, res.img); y > 14 {
		t.Errorf("forced: Y error %.2f", y)
	}
	// forced to a different mode: the VIS is ignored, nothing decodes
	other, _ := ParseMode("Scottie1")
	pcm = Encode(m, ref, Rate)
	if res := decodeAll(t, channel(pcm, 25, Rate, Rate, 4), Rate, other); res.img != nil {
		t.Error("decoded a Robot36 picture in forced Scottie1 mode")
	}
}

func TestClockOffsets(t *testing.T) {
	for _, c := range []struct {
		mode string
		off  float64
	}{{"Martin1", 1.01}, {"PD90", 0.99}, {"Scottie1", 0.992}, {"Robot36", 1.008}} {
		m, _ := ParseMode(c.mode)
		ref := TestPattern(m.Width, m.Height)
		res := decodeAll(t, channel(Encode(m, ref, Rate*c.off), 20, Rate, Rate, 9), Rate, nil)
		if res.img == nil || res.mode != m {
			t.Fatalf("%s at %.3f: no picture", c.mode, c.off)
		}
		y, _ := errors(ref, res.img)
		t.Logf("%s at clock x%.3f: Y error %.2f", c.mode, c.off, y)
		if y > 8 {
			t.Errorf("%s at %.3f: Y error %.2f", c.mode, c.off, y)
		}
	}
}

func TestNoSignal(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	pcm := make([]int16, 30*Rate)
	for i := range pcm {
		pcm[i] = int16(rng.NormFloat64() * 3000)
	}
	if res := decodeAll(t, pcm, Rate, nil); res.mode != nil {
		t.Errorf("found %s in noise", res.mode.Name)
	}
}

func TestTwoPicturesAndPartial(t *testing.T) {
	m, _ := ParseMode("Robot36")
	ref := TestPattern(m.Width, m.Height)
	one := Encode(m, ref, Rate)
	pcm := append(append(channel(one, 30, Rate, Rate, 6), channel(one, 30, 0, Rate, 7)...), channel(one[:len(one)/2], 30, Rate, 0, 8)...)
	var got []int
	d := NewDecoder(Rate, nil)
	d.OnImage = func(_ *image.RGBA, rows int) { got = append(got, rows) }
	for i := 0; i < len(pcm); i += 777 {
		d.Feed(pcm[i:min(i+777, len(pcm))])
	}
	d.Finish()
	if len(got) != 3 || got[0] != 240 || got[1] != 240 || got[2] < 100 || got[2] > 130 {
		t.Errorf("images (rows) = %v, want [240 240 ~120]", got)
	}
}

func TestWritePNG(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.png")
	if err := WritePNG(p, TestPattern(16, 8)); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		t.Fatal("no file")
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Errorf("leftover temp files: %v", ents)
	}
}
