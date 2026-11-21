package dsp

import "testing"

func TestFindPeaks(t *testing.T) {
	db := make([]float64, 100)
	for i := range db {
		db[i] = -70
	}
	db[30], db[31], db[32] = -50, -40, -50 // strong
	db[34] = -55                           // shoulder, merged into the strong peak
	db[70] = -60                           // weak
	db[2] = -10                            // in the edge zone, ignored
	ps := FindPeaks(db, 518.0e6, 0.01e6, 10, 6, 5, 0.05)
	if len(ps) != 2 {
		t.Fatalf("peaks = %+v", ps)
	}
	if ps[0].FreqHz != 518.31e6 || ps[0].SNR != 30 {
		t.Fatalf("first = %+v", ps[0])
	}
	if ps[1].FreqHz != 518.7e6 {
		t.Fatalf("second = %+v", ps[1])
	}
	// 518.4 MHz = 18 * 28.8 MHz is a crystal spur; 518.31 is 9 bins away, not flagged
	if ps[0].Spur {
		t.Fatalf("518.31 MHz flagged as spur")
	}
	sp := FindPeaks(func() []float64 { d := make([]float64, 100); d[40] = 20; return d }(), 518.0e6, 0.01e6, 1, 6, 5, 0.05)
	if len(sp) != 1 || !sp[0].Spur {
		t.Fatalf("518.4 MHz not flagged: %+v", sp)
	}
}
