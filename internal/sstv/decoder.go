package sstv

import (
	"image"
	"math"
	"math/cmplx"
)

// Decoder turns audio chunks into SSTV pictures. It is not safe for
// concurrent use; the callbacks run inside Feed on the caller's goroutine.
type Decoder struct {
	// OnStart is called when a picture begins (VIS found, or sync train in forced mode).
	OnStart func(m *Mode)
	// OnLine is called after each decoded unit with the number of image rows
	// complete so far. Image returns the (partial) picture.
	OnLine func(rows int)
	// OnImage is called when a picture ends: complete, or abandoned after the
	// signal was lost (only when at least 16 rows were decoded).
	OnImage func(img *image.RGBA, rows int)

	forced *Mode
	rs     *resampler

	// Signal history. Absolute sample index i lives at slice position i-off.
	//   x:    audio at Rate
	//   cum:  cum[i] = sum of p[k], k < i, p[k] = z[k]*conj(z[k-1])  (z = analytic signal)
	//   lowc: lowc[i] = number of k < i with instantaneous frequency < 1350 Hz
	off        int64
	x          []float64
	cum        []complex128
	lowc       []int32
	nx, nz, nf int64
	zprev      complex128
	kern       []complex128

	// search state
	scanVIS, scanTrain int64

	// decode state
	mode    *Mode
	img     *image.RGBA
	u       int
	rows    int
	expEnd0 float64
	pts     []syncPt
	miss    int
	lastEnd float64
	cr, cb  []float64 // Robot 36 running chroma
	prevY   []float64
	prevRow int
	haveCr  bool
	haveCb  bool
}

type syncPt struct {
	u int
	e float64
}

const (
	kPerMs    = Rate / 1000.0
	lowThresh = 1350.0
	maxMiss   = 12
	fitWindow = 80
)

func ms(v float64) int64 { return int64(math.Round(v * kPerMs)) }

// NewDecoder creates a decoder for audio at inRate Hz. forced is the mode to
// decode without needing a VIS header (nil: detect the mode from the VIS).
func NewDecoder(inRate int, forced *Mode) *Decoder {
	d := &Decoder{forced: forced, rs: newResampler(float64(inRate))}
	d.cum = []complex128{0}
	d.lowc = []int32{0, 0}
	d.nf = 1
	// Analytic-signal FIR: a windowed lowpass shifted to 1700 Hz (a Hilbert
	// transformer restricted to the SSTV band), 63 taps.
	const taps, centre, cutoff = 63, 1700.0, 850.0
	m := taps / 2
	d.kern = make([]complex128, taps)
	for k := -m; k <= m; k++ {
		w := 0.54 + 0.46*math.Cos(math.Pi*float64(k)/float64(m+1))
		lp := 2 * cutoff / Rate * sinc(2*cutoff*float64(k)/Rate)
		d.kern[k+m] = cmplx.Rect(2*lp*w, 2*math.Pi*centre*float64(k)/Rate)
	}
	return d
}

// Image returns the picture being decoded (rows not yet received are black), or nil.
func (d *Decoder) Image() *image.RGBA { return d.img }

// Mode returns the mode being decoded, or nil.
func (d *Decoder) Mode() *Mode { return d.mode }

// Rows returns the number of image rows decoded so far.
func (d *Decoder) Rows() int { return d.rows }

// Feed processes audio at the input rate given to NewDecoder.
func (d *Decoder) Feed(pcm []int16) {
	d.rs.push(pcm, func(v float64) {
		d.x = append(d.x, v)
		d.nx++
	})
	d.analyze()
	d.run()
	d.trim()
}

// Finish ends the picture in progress (if any), emitting it.
func (d *Decoder) Finish() {
	if d.mode != nil {
		d.endImage(true)
	}
}

// analyze extends the analytic signal, the cumulative lag-1 product and the
// low-frequency flags over the newly arrived samples.
func (d *Decoder) analyze() {
	m := len(d.kern) / 2
	for d.nz+int64(m) < d.nx {
		n := d.nz
		var z complex128
		for k := -m; k <= m; k++ {
			idx := n - int64(k)
			if idx < 0 {
				continue
			}
			z += d.kern[k+m] * complex(d.x[idx-d.off], 0)
		}
		var p complex128
		if n > 0 {
			p = z * complex(real(d.zprev), -imag(d.zprev))
		}
		d.zprev = z
		d.cum = append(d.cum, d.cum[len(d.cum)-1]+p)
		d.nz++
		if d.nz >= 3 {
			m2 := d.nz - 2
			s := d.cumAtI(m2+2) - d.cumAtI(m2-1)
			hz := math.Atan2(imag(s), real(s)) * Rate / (2 * math.Pi)
			c := d.lowc[len(d.lowc)-1]
			if hz < lowThresh {
				c++
			}
			d.lowc = append(d.lowc, c)
			d.nf = d.nz - 1
		}
	}
}

func (d *Decoder) cumAtI(i int64) complex128 { return d.cum[i-d.off] }

// cumAt is cum at a fractional index (the lag products are spread evenly).
func (d *Decoder) cumAt(x float64) complex128 {
	i := int64(math.Floor(x))
	if i >= d.nz {
		return d.cum[d.nz-d.off]
	}
	c := d.cum[i-d.off]
	if fr := x - float64(i); fr > 0 {
		c += complex(fr, 0) * (d.cum[i+1-d.off] - c)
	}
	return c
}

// hz is the mean instantaneous frequency over the time window [a, b) in samples.
// p[k] covers the interval (k-1, k], so its centre is k-0.5.
func (d *Decoder) hz(a, b float64) float64 {
	s := d.cumAt(b+0.5) - d.cumAt(a+0.5)
	return math.Atan2(imag(s), real(s)) * Rate / (2 * math.Pi)
}

func (d *Decoder) meanHz(a, b int64) float64 {
	s := d.cumAtI(b) - d.cumAtI(a)
	return math.Atan2(imag(s), real(s)) * Rate / (2 * math.Pi)
}

// lowCount counts low-frequency samples with f-index in [a, b).
func (d *Decoder) lowCount(a, b int64) int {
	if a < d.off {
		a = d.off
	}
	if b > d.nf {
		b = d.nf
	}
	if b <= a {
		return 0
	}
	return int(d.lowc[b-d.off] - d.lowc[a-d.off])
}

func (d *Decoder) lowFrac(a, b int64) float64 {
	if b <= a {
		return 0
	}
	return float64(d.lowCount(a, b)) / float64(b-a)
}

func (d *Decoder) trim() {
	var keep int64
	if d.mode != nil {
		keep = int64(d.lastEnd-(d.mode.SyncOffMs+d.mode.SyncMs)*kPerMs*1.03) - ms(60+0.015*d.mode.UnitMs)
	} else {
		keep = d.scanVIS
		if d.forced != nil && d.scanTrain < keep {
			keep = d.scanTrain
		}
		keep -= ms(700)
	}
	if lim := d.nz - int64(len(d.kern)) - 1; keep > lim {
		keep = lim
	}
	drop := keep - d.off
	if drop < 1<<15 {
		return
	}
	d.x = append(d.x[:0], d.x[drop:]...)
	d.cum = append(d.cum[:0], d.cum[drop:]...)
	d.lowc = append(d.lowc[:0], d.lowc[drop:]...)
	d.off = keep
}

// run advances the state machine as far as the data allows.
func (d *Decoder) run() {
	for {
		if d.mode == nil {
			if !d.search() {
				return
			}
			continue
		}
		if !d.decodeUnit() {
			return
		}
	}
}

// search looks for the start of a picture. It reports whether a picture began.
func (d *Decoder) search() bool {
	if m, l0, ok := d.searchVIS(); ok {
		d.begin(m, float64(l0)+(m.SyncOffMs+m.SyncMs)*kPerMs)
		return true
	}
	if d.forced != nil {
		if end, ok := d.searchTrain(d.forced); ok {
			d.begin(d.forced, end)
			return true
		}
	}
	return false
}

func (d *Decoder) bitsFrom(t0 int64) (code int, ok bool) {
	ones := 0
	for k := 0; k < 8; k++ {
		c := t0 + ms(30*float64(k+1)+15)
		hz := d.meanHz(c-ms(8), c+ms(8))
		if hz < 1000 || hz > 1400 {
			return 0, false
		}
		if hz < 1200 {
			ones++
			if k < 7 {
				code |= 1 << k
			}
		}
	}
	return code, ones%2 == 0
}

func near(v, target, tol float64) bool { return math.Abs(v-target) <= tol }

// searchVIS finds a VIS header: 300 ms 1900 Hz, 10 ms 1200 Hz break, 300 ms
// 1900 Hz, then the start bit. It returns the mode and the sample where the
// picture data begins (the end of the stop bit).
func (d *Decoder) searchVIS() (*Mode, int64, bool) {
	l := ms(2)
	need := ms(312) + l
	start := d.scanVIS
	if start < d.off+l {
		start = d.off + l
	}
	for i := start; i+need < d.nf; i++ {
		if d.lowFrac(i, i+l) < 0.7 || d.lowFrac(i-l, i) > 0.2 {
			continue
		}
		// refine the falling edge
		t0, best := i, -2.0
		for j := i; j < i+l; j++ {
			if s := d.lowFrac(j, j+l) - d.lowFrac(j-l, j); s > best {
				t0, best = j, s
			}
		}
		i = t0
		if m, ok := d.verifyVIS(t0); ok {
			d.scanVIS = t0 + ms(300)
			return m, t0 + ms(300), true
		}
		i = t0 + 2*l
	}
	if n := d.nf - need - 1; n > d.scanVIS {
		d.scanVIS = n
	}
	return nil, 0, false
}

func (d *Decoder) verifyVIS(t0 int64) (*Mode, bool) {
	if t0-ms(300) < d.off {
		return nil, false
	}
	// leader 2 steady at 1900 Hz
	if !near(d.meanHz(t0-ms(280), t0-ms(20)), FreqLeader, 150) || d.lowFrac(t0-ms(280), t0-ms(20)) > 0.05 {
		return nil, false
	}
	// the break somewhere 285-335 ms before the start bit
	brk := false
	for j := t0 - ms(335); j <= t0-ms(285); j += ms(1) {
		if d.lowFrac(j, j+ms(6)) >= 0.7 {
			brk = true
			break
		}
	}
	if !brk {
		return nil, false
	}
	if t0-ms(590) >= d.off && !near(d.meanHz(t0-ms(590), t0-ms(345)), FreqLeader, 200) {
		return nil, false
	}
	if !near(d.meanHz(t0+ms(4), t0+ms(26)), FreqSync, 150) || !near(d.meanHz(t0+ms(274), t0+ms(296)), FreqSync, 150) {
		return nil, false
	}
	code, ok := d.bitsFrom(t0)
	if !ok {
		return nil, false
	}
	m := ByVIS(code)
	if m == nil || (d.forced != nil && d.forced != m) {
		return nil, false
	}
	return m, true
}

// syncParams returns the pulse and porch lengths in samples for the sync scoring.
func syncParams(m *Mode) (s, p int64) {
	s = int64(0.9 * m.SyncMs * kPerMs)
	p = int64(m.PorchMs * kPerMs)
	if p < 4 {
		p = 4
	}
	if p > 14 {
		p = 14
	}
	return
}

// syncScoreInt scores a sync pulse ending with its last low sample at t-1:
// low samples before t plus high samples right after.
func (d *Decoder) syncScoreInt(t, s, p int64) int {
	return d.lowCount(t-s, t) + int(p) - d.lowCount(t+1, t+1+p)
}

// searchSync looks for the sync pulse end in [centre-hw, centre+hw] (sample
// time). It returns the pulse end time and a 0..1 score.
func (d *Decoder) searchSync(m *Mode, centre float64, hw int64) (float64, float64) {
	s, p := syncParams(m)
	c := int64(math.Round(centre)) + 1
	best, n, sum := -1, 0, 0.0
	for t := c - hw; t <= c+hw; t++ {
		if t-s < d.off || t+1+p > d.nf {
			continue
		}
		sc := d.syncScoreInt(t, s, p)
		if sc > best {
			best, n, sum = sc, 0, 0
		}
		if sc == best {
			n++
			sum += float64(t)
		}
	}
	if n == 0 {
		return centre, 0
	}
	return sum/float64(n) - 1, float64(best) / float64(s+p)
}

// searchTrain finds a run of five evenly spaced sync pulses of mode m
// (decoding without a VIS). It returns the end of the first pulse.
func (d *Decoder) searchTrain(m *Mode) (float64, bool) {
	s, p := syncParams(m)
	nom := m.UnitMs * kPerMs
	span := int64(4.2*nom) + ms(20)
	i := d.scanTrain
	if i < d.off+s {
		i = d.off + s
	}
	for ; i+span < d.nf; i++ {
		if float64(d.syncScoreInt(i, s, p)) < 0.85*float64(s+p) {
			continue
		}
		e, sc := d.searchSync(m, float64(i)-1, 4)
		if sc < 0.85 {
			continue
		}
		first, cur, ok := e, e, true
		for k := 1; k <= 4 && ok; k++ {
			e2, sc2 := d.searchSync(m, cur+nom, ms(3)+int64(0.012*nom))
			if sc2 < 0.7 {
				ok = false
				break
			}
			cur = e2
		}
		if ok {
			d.scanTrain = int64(cur)
			return first, true
		}
		i += ms(1)
	}
	if n := d.nf - span - 1; n > d.scanTrain {
		d.scanTrain = n
	}
	return 0, false
}

func (d *Decoder) begin(m *Mode, firstEnd float64) {
	d.mode = m
	d.img = image.NewRGBA(image.Rect(0, 0, m.Width, m.Height))
	for i := 3; i < len(d.img.Pix); i += 4 {
		d.img.Pix[i] = 255
	}
	d.u, d.rows, d.miss = 0, 0, 0
	d.expEnd0, d.lastEnd = firstEnd, firstEnd // lastEnd: where the next sync is expected
	d.pts = d.pts[:0]
	d.cr, d.cb, d.prevY = nil, nil, nil
	d.haveCr, d.haveCb = false, false
	if d.OnStart != nil {
		d.OnStart(m)
	}
}

// endImage finishes the current picture and returns to searching.
func (d *Decoder) endImage(emit bool) {
	m, img, rows := d.mode, d.img, d.rows
	pos := int64(d.lastEnd - m.UnitMs*kPerMs) // the last unit's sync
	if d.u == 0 {
		pos = int64(d.lastEnd)
	}
	if rows >= m.Height {
		pos += int64((m.UnitMs - m.SyncOffMs - m.SyncMs) * kPerMs)
	}
	d.scanVIS = max(d.scanVIS, pos)
	d.scanTrain = max(d.scanTrain, pos)
	d.mode = nil
	if emit && d.OnImage != nil && (rows >= 16 || rows >= m.Height) {
		d.OnImage(img, rows)
	}
}

// fit returns the least-squares line through the recent sync points.
func (d *Decoder) fit() (a, b float64, ok bool) {
	pts := d.pts
	if len(pts) > fitWindow {
		pts = pts[len(pts)-fitWindow:]
	}
	if len(pts) < 2 {
		return 0, 0, false
	}
	var su, se, suu, sue float64
	for _, q := range pts {
		u := float64(q.u)
		su += u
		se += q.e
		suu += u * u
		sue += u * q.e
	}
	n := float64(len(pts))
	den := n*suu - su*su
	if den == 0 {
		return 0, 0, false
	}
	b = (n*sue - su*se) / den
	a = (se - b*su) / n
	return a, b, true
}

// decodeUnit locates the sync pulse of unit d.u and decodes the unit. It
// returns false when it needs more audio.
func (d *Decoder) decodeUnit() bool {
	m := d.mode
	nom := m.UnitMs * kPerMs
	u := d.u
	if u >= m.Units() {
		d.endImage(true)
		return true
	}
	// prediction of the sync end
	pred := d.expEnd0 + float64(u)*nom
	if a, b, ok := d.fit(); ok {
		if b > nom*0.97 && b < nom*1.03 {
			pred = a + b*float64(u)
		}
	} else if len(d.pts) == 1 {
		pred = d.pts[0].e + float64(u-d.pts[0].u)*nom
	}
	hw := ms(math.Max(8, 6+0.015*m.UnitMs))
	if u == 0 {
		hw = ms(25)
	}
	tail := (m.UnitMs-m.SyncOffMs-m.SyncMs)*kPerMs*1.03 + 8
	if int64(pred)+hw+int64(tail)+64 > d.nz {
		return false
	}
	e, sc := d.searchSync(m, pred, hw)
	detected := sc >= 0.7
	if detected && len(d.pts) >= 6 && math.Abs(e-pred) > float64(ms(4)) {
		detected = false
	}
	use := pred
	if detected {
		d.miss = 0
		d.pts = append(d.pts, syncPt{u, e})
		use = e
		if a, b, ok := d.fit(); ok && len(d.pts) >= 5 && b > nom*0.97 && b < nom*1.03 {
			use = a + b*float64(u)
		}
	} else {
		d.miss++
		if d.miss > maxMiss {
			d.endImage(true)
			return true
		}
	}
	scale := 1.0
	if _, b, ok := d.fit(); ok && len(d.pts) >= 4 && b > nom*0.97 && b < nom*1.03 {
		scale = b / nom
	}
	d.lastEnd = use + nom
	d.renderUnit(u, use, scale)
	d.u++
	d.rows = min(m.Height, d.u*m.RowsPerUnit)
	if d.OnLine != nil {
		d.OnLine(d.rows)
	}
	if d.u >= m.Units() {
		d.endImage(true)
	}
	return true
}

func level(hz float64) float64 {
	v := (hz - FreqBlack) / (FreqWhite - FreqBlack) * 255
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// scan returns the pixel levels of component c for the unit that starts at
// sample position l, with time scale s.
func (d *Decoder) scan(c Comp, l, s float64) []float64 {
	out := make([]float64, c.N)
	pt := c.DurMs / float64(c.N) * kPerMs * s
	t0 := l + c.OffMs*kPerMs*s
	for i := range out {
		a := t0 + float64(i)*pt
		out[i] = level(d.hz(a, a+pt) * s)
	}
	return out
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func (d *Decoder) setPix(row, x int, r, g, b float64) {
	o := row*d.img.Stride + x*4
	d.img.Pix[o], d.img.Pix[o+1], d.img.Pix[o+2], d.img.Pix[o+3] = clamp8(r), clamp8(g), clamp8(b), 255
}

func (d *Decoder) setYCrCb(row, x int, y, cr, cb float64) {
	d.setPix(row, x, y+1.4*(cr-127.5), y-0.71*(cr-127.5)-0.33*(cb-127.5), y+1.78*(cb-127.5))
}

// renderUnit decodes unit u whose sync pulse ends at sample e.
func (d *Decoder) renderUnit(u int, e, s float64) {
	m := d.mode
	l := e - (m.SyncOffMs+m.SyncMs)*kPerMs*s
	switch m.Format {
	case FormatPD:
		var ch [4][]float64
		for i, c := range m.Comps {
			ch[i] = d.scan(c, l, s)
		}
		for x := 0; x < m.Width; x++ {
			d.setYCrCb(2*u, x, ch[0][x], ch[1][x], ch[2][x])
			d.setYCrCb(2*u+1, x, ch[3][x], ch[1][x], ch[2][x])
		}
	case FormatRGB:
		var r, g, b []float64
		for _, c := range m.Comps {
			v := d.scan(c, l, s)
			switch c.Ch {
			case ChR:
				r = v
			case ChG:
				g = v
			case ChB:
				b = v
			}
		}
		for x := 0; x < m.Width; x++ {
			d.setPix(u, x, r[x], g[x], b[x])
		}
	case FormatRobot36:
		y := d.scan(m.Comps[0], l, s)
		c := d.scan(m.Comps[1], l, s)
		sep := d.hz(l+m.SepOffMs*kPerMs*s+0.5*kPerMs, l+(m.SepOffMs+m.SepDurMs)*kPerMs*s-0.5*kPerMs) * s
		isCr := u%2 == 0
		if sep > 1650 && sep < 2600 {
			isCr = false
		} else if sep > 1100 && sep <= 1650 {
			isCr = true
		}
		if isCr {
			d.cr, d.haveCr = c, true
		} else {
			d.cb, d.haveCb = c, true
		}
		d.paintRobot(u, y)
		if !isCr && u > 0 && d.prevY != nil && d.prevRow == u-1 {
			d.paintRobot(u-1, d.prevY)
		}
		d.prevY, d.prevRow = y, u
	}
}

func (d *Decoder) paintRobot(row int, y []float64) {
	for x := 0; x < d.mode.Width; x++ {
		cr, cb := 127.5, 127.5
		if d.haveCr {
			cr = d.cr[x/2]
		}
		if d.haveCb {
			cb = d.cb[x/2]
		}
		d.setYCrCb(row, x, y[x], cr, cb)
	}
}
