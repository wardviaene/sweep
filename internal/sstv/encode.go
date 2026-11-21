package sstv

import (
	"image"
	"image/color"
	"math"
	"sort"
)

// Encode synthesizes the audio of one picture in mode m at the given sample
// rate: 1 s of VIS leader/header, then the scan lines. It exists to test the
// decoder (and to drive the synthetic radio source); img is scaled (nearest
// neighbour) to the mode's size. Passing a rate slightly off from the decoder's
// models a sample-clock error.
func Encode(m *Mode, img image.Image, rate float64) []int16 {
	src := scaleTo(img, m.Width, m.Height)
	var s toneSeq
	// VIS header
	s.add(FreqLeader, visLeaderMs)
	s.add(FreqSync, visBreakMs)
	s.add(FreqLeader, visLeaderMs)
	s.add(FreqSync, visBitMs) // start bit
	ones := 0
	for k := 0; k < 8; k++ {
		bit := (m.VIS >> k) & 1
		if k == 7 {
			bit = ones & 1 // even parity
		}
		ones += bit
		if bit == 1 {
			s.add(FreqBit1, visBitMs)
		} else {
			s.add(FreqBit0, visBitMs)
		}
	}
	s.add(FreqSync, visBitMs) // stop bit

	type seg struct {
		off, dur, f float64
	}
	for u := 0; u < m.Units(); u++ {
		var segs []seg
		segs = append(segs, seg{m.SyncOffMs, m.SyncMs, FreqSync})
		if m.Format == FormatRobot36 {
			f := FreqBlack
			if u%2 == 1 {
				f = FreqWhite
			}
			segs = append(segs, seg{m.SepOffMs, m.SepDurMs, f}, seg{m.SepOffMs + m.SepDurMs, 1.5, FreqLeader})
		}
		for _, c := range m.Comps {
			v := compLevels(m, c, src, u)
			pt := c.DurMs / float64(c.N)
			for i, lv := range v {
				segs = append(segs, seg{c.OffMs + float64(i)*pt, pt, FreqBlack + (FreqWhite-FreqBlack)*lv/255})
			}
		}
		sort.Slice(segs, func(i, j int) bool { return segs[i].off < segs[j].off })
		base := s.t
		cur := 0.0
		for _, g := range segs {
			if g.off > cur+1e-6 {
				s.addAbs(FreqBlack, base+g.off/1000)
			}
			s.addAbs(g.f, base+(g.off+g.dur)/1000)
			cur = g.off + g.dur
		}
		if cur < m.UnitMs-1e-6 {
			s.addAbs(FreqBlack, base+m.UnitMs/1000)
		}
	}
	return s.render(rate)
}

// toneSeq is a piecewise-constant frequency track; times in seconds.
type toneSeq struct {
	ends  []float64
	freqs []float64
	t     float64
}

func (s *toneSeq) add(f, durMs float64) { s.addAbs(f, s.t+durMs/1000) }

func (s *toneSeq) addAbs(f, end float64) {
	if end <= s.t {
		return
	}
	s.ends = append(s.ends, end)
	s.freqs = append(s.freqs, f)
	s.t = end
}

func (s *toneSeq) render(rate float64) []int16 {
	n := int(s.t * rate)
	out := make([]int16, n)
	var phase float64
	idx := 0
	for i := range out {
		t := (float64(i) + 0.5) / rate
		for idx < len(s.ends)-1 && s.ends[idx] <= t {
			idx++
		}
		phase += 2 * math.Pi * s.freqs[idx] / rate
		if phase > 2*math.Pi {
			phase -= 2 * math.Pi
		}
		out[i] = int16(math.Sin(phase) * 0.8 * 32767)
	}
	return out
}

func scaleTo(img image.Image, w, h int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(x, y, img.At(b.Min.X+x*b.Dx()/w, b.Min.Y+y*b.Dy()/h))
		}
	}
	return out
}

func rgbAt(img *image.RGBA, x, y int) (r, g, b float64) {
	c := img.RGBAAt(x, y)
	return float64(c.R), float64(c.G), float64(c.B)
}

func lumaCr(r, g, b float64) (y, cr, cb float64) {
	y = 0.299*r + 0.587*g + 0.114*b
	return y, (r-y)/1.4 + 127.5, (b-y)/1.78 + 127.5
}

// compLevels returns the 0..255 levels of component c of unit u.
func compLevels(m *Mode, c Comp, img *image.RGBA, u int) []float64 {
	out := make([]float64, c.N)
	clip := func(v float64) float64 { return math.Max(0, math.Min(255, v)) }
	switch m.Format {
	case FormatRGB:
		for x := range out {
			r, g, b := rgbAt(img, x, u)
			switch c.Ch {
			case ChR:
				out[x] = r
			case ChG:
				out[x] = g
			case ChB:
				out[x] = b
			}
		}
	case FormatPD:
		for x := range out {
			r0, g0, b0 := rgbAt(img, x, 2*u)
			r1, g1, b1 := rgbAt(img, x, 2*u+1)
			y0, cr0, cb0 := lumaCr(r0, g0, b0)
			y1, cr1, cb1 := lumaCr(r1, g1, b1)
			switch c.Ch {
			case ChY:
				out[x] = y0
			case ChY2:
				out[x] = y1
			case ChCr:
				out[x] = clip((cr0 + cr1) / 2)
			case ChCb:
				out[x] = clip((cb0 + cb1) / 2)
			}
		}
	case FormatRobot36:
		r0 := u &^ 1
		for x := range out {
			if c.Ch == ChY {
				r, g, b := rgbAt(img, x, u)
				out[x], _, _ = lumaCr(r, g, b)
				continue
			}
			// chroma: 2x2 average over the row pair
			var cr, cb float64
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					pr, pg, pb := rgbAt(img, 2*x+dx, r0+dy)
					_, a, b := lumaCr(pr, pg, pb)
					cr += a / 4
					cb += b / 4
				}
			}
			if u%2 == 0 {
				out[x] = clip(cr)
			} else {
				out[x] = clip(cb)
			}
		}
	}
	return out
}

// TestPattern returns a w x h picture of color bars (top), a gray ramp and a
// color ramp, for tests.
func TestPattern(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bars := []color.RGBA{{255, 255, 255, 255}, {255, 255, 0, 255}, {0, 255, 255, 255}, {0, 255, 0, 255},
		{255, 0, 255, 255}, {255, 0, 0, 255}, {0, 0, 255, 255}, {0, 0, 0, 255}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.RGBA
			switch {
			case y < h*55/100:
				c = bars[x*len(bars)/w]
			case y < h*78/100:
				v := uint8(x * 255 / (w - 1))
				c = color.RGBA{v, v, v, 255}
			default:
				c = color.RGBA{uint8(x * 255 / (w - 1)), uint8(255 - x*255/(w-1)), uint8(128 + (y-h*78/100)*127/(h-h*78/100)), 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}
