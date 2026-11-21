package sstv

import "math"

// resampler converts a stream of int16 audio at any rate to float64 at Rate
// using a windowed-sinc (Lanczos) kernel, with the cutoff at the lower of the
// two Nyquist frequencies. It is streaming: call push with consecutive chunks.
type resampler struct {
	step  float64 // input samples per output sample
	scale float64 // kernel stretch (>1 when downsampling)
	half  int64
	pos   float64 // input coordinate of the next output sample
	base  int64   // input index of buf[0]
	buf   []float64
	pass  bool
}

const lanczosA = 6

func newResampler(inRate float64) *resampler {
	r := &resampler{step: inRate / Rate}
	if math.Abs(r.step-1) < 1e-9 {
		r.pass = true
		return r
	}
	r.scale = math.Max(1, r.step)
	r.half = int64(math.Ceil(lanczosA * r.scale))
	r.base = -r.half
	r.buf = make([]float64, r.half) // zeros before the start
	return r
}

func sinc(x float64) float64 {
	if math.Abs(x) < 1e-9 {
		return 1
	}
	x *= math.Pi
	return math.Sin(x) / x
}

// push resamples one chunk and passes the output samples to emit.
func (r *resampler) push(in []int16, emit func(v float64)) {
	if r.pass {
		for _, v := range in {
			emit(float64(v) / 32768)
		}
		return
	}
	for _, v := range in {
		r.buf = append(r.buf, float64(v)/32768)
	}
	end := r.base + int64(len(r.buf))
	for {
		fl := int64(math.Floor(r.pos))
		if fl+r.half >= end {
			break
		}
		var acc float64
		for k := fl - r.half + 1; k <= fl+r.half; k++ {
			d := r.pos - float64(k)
			ad := math.Abs(d) / r.scale
			if ad >= lanczosA {
				continue
			}
			acc += r.buf[k-r.base] * sinc(d/r.scale) * sinc(ad/lanczosA)
		}
		emit(acc / r.scale)
		r.pos += r.step
	}
	// drop what no later output can touch
	if drop := int64(math.Floor(r.pos)) - r.half - r.base; drop > 4096 {
		r.buf = append(r.buf[:0], r.buf[drop:]...)
		r.base += drop
	}
}
