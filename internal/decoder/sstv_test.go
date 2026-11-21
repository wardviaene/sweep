package decoder

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wardviaene/sweep/internal/radio"
	"github.com/wardviaene/sweep/internal/sstv"
)

type nopSink struct{}

func (nopSink) Meta(radio.Meta) {}
func (nopSink) Frame([]byte)    {}

func TestSSTVOptions(t *testing.T) {
	var o Options
	if err := json.Unmarshal([]byte(`{"freq_hz":145800000,"mode":"PD120"}`), &o); err != nil || o.Mode != "PD120" {
		t.Fatalf("%+v %v", o, err)
	}
	found := false
	for _, n := range Names {
		found = found || n == "sstv"
	}
	if !found {
		t.Fatal("sstv not in Names")
	}
}

func TestUniqueName(t *testing.T) {
	d := t.TempDir()
	a := uniqueName(d, "x", ".png")
	os.WriteFile(a, nil, 0o644)
	b := uniqueName(d, "x", ".png")
	if a == b || !strings.HasSuffix(b, "x-2.png") {
		t.Fatalf("%s %s", a, b)
	}
}

// TestSSTVEndToEnd FM-modulates a Robot 36 picture onto 145.8 MHz in the
// synthetic source, runs the sstv decoder through the manager and waits for
// the finished picture (real time: about 40 to 80 s).
func TestSSTVEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about a minute (the synthetic source runs in real time)")
	}
	t.Setenv("PATH", t.TempDir())
	mode, _ := sstv.ParseMode("Robot36")
	ref := sstv.TestPattern(mode.Width, mode.Height)
	pcm := sstv.Encode(mode, ref, sstv.Rate)

	gain := 20.7
	r := radio.New(radio.Config{Synth: true, SynthFM: []radio.SynthFM{{FreqHz: 145_800_000, PCM: pcm, Rate: sstv.Rate}},
		CenterHz: 145_000_000, SampleRate: 1_000_000, GainDB: &gain, FFTSize: 2048, FPS: 20, MinDB: -90, MaxDB: -20}, nopSink{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	for end := time.Now().Add(10 * time.Second); !r.Status().Connected; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("radio not connected")
		}
	}
	ev := NewEvents()
	m := NewManager(r, ev)
	m.SSTVDir = filepath.Join(t.TempDir(), "sstv")
	defer m.Stop()
	if err := m.Start("sstv", Options{FreqHz: 145_800_000}); err != nil {
		t.Fatal(err)
	}
	sub, unsub := ev.Subscribe(0)
	defer unsub()

	progress := 0
	timeout := time.After(150 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatalf("no image after 150 s (status %+v, %d progress events)", m.Status(), progress)
		case b := <-sub:
			var e struct {
				Decoder, Kind string
				Data          map[string]any
			}
			if err := json.Unmarshal(b, &e); err != nil || e.Decoder != "sstv" {
				continue
			}
			switch e.Kind {
			case "progress":
				progress++
				if !strings.HasPrefix(e.Data["url"].(string), "/api/files/sstv/") {
					t.Errorf("progress url %v", e.Data["url"])
				}
			case "image":
				if e.Data["mode"] != "Robot36" || e.Data["lines"].(float64) != 240 || e.Data["width"].(float64) != 320 {
					t.Fatalf("image event %v", e.Data)
				}
				path := e.Data["path"].(string)
				if !strings.HasPrefix(filepath.Base(path), time.Now().UTC().Format("2006")) || !strings.HasSuffix(path, "_robot36.png") {
					t.Errorf("file name %s", path)
				}
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				img, err := png.Decode(f)
				if err != nil {
					t.Fatal(err)
				}
				mae := meanLumaError(ref, img)
				t.Logf("image %v, %d progress events, mean luminance error %.1f", e.Data["url"], progress, mae)
				if progress < 5 {
					t.Errorf("only %d progress events", progress)
				}
				if mae > 20 {
					t.Errorf("luminance error %.1f", mae)
				}
				return
			}
		}
	}
}

func meanLumaError(a *image.RGBA, b image.Image) float64 {
	var sum float64
	n := 0
	l := func(r, g, bl uint32) float64 { return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257 }
	for y := 0; y < a.Rect.Dy(); y++ {
		for x := 0; x < a.Rect.Dx(); x++ {
			r1, g1, b1, _ := a.At(x, y).RGBA()
			r2, g2, b2, _ := b.At(x, y).RGBA()
			sum += math.Abs(l(r1, g1, b1) - l(r2, g2, b2))
			n++
		}
	}
	return sum / float64(n)
}
