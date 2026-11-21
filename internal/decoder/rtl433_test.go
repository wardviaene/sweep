package decoder

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"strings"
	"testing"
)

// ev1527 builds cu8 IQ of an OOK remote (PT2262/EV1527 style) sending a
// 24-bit code: '1' = long on + short off, '0' = short on + long off, then a
// sync gap. The carrier sits at offsetHz from the center.
func ev1527(code uint32, rate, offsetHz float64, repeats int) []byte {
	const short, long = 350e-6, 1050e-6
	var iq []byte
	ph := 0.0
	emit := func(on bool, secs float64) {
		n := int(secs * rate)
		for i := 0; i < n; i++ {
			a := 0.0
			if on {
				a = 0.8
			}
			ph += 2 * math.Pi * offsetHz / rate
			iq = append(iq, byte(127.5+127*a*math.Cos(ph)), byte(127.5+127*a*math.Sin(ph)))
		}
	}
	emit(false, 0.05)
	for r := 0; r < repeats; r++ {
		for b := 23; b >= 0; b-- {
			if code>>b&1 == 1 {
				emit(true, long)
				emit(false, short)
			} else {
				emit(true, short)
				emit(false, long)
			}
		}
		emit(true, short)
		emit(false, 31*short)
	}
	emit(false, 0.2)
	return iq
}

// TestRTL433Pipeline feeds a synthetic remote to rtl_433 with the decoder's
// exact arguments. Skipped when rtl_433 is not installed.
func TestRTL433Pipeline(t *testing.T) {
	bin, err := exec.LookPath("rtl_433")
	if err != nil {
		t.Skip("rtl_433 not installed")
	}
	const rate = 1_024_000
	// A flex decoder for this pulse shape makes the result independent of
	// which built-in protocols rtl_433 enables by default.
	args := append(rtl433Args(rate, 433_920_000), "-X", "n=sweep-test,m=OOK_PWM,s=350,l=1050,r=8000")
	cmd := exec.Command(bin, args...)
	cmd.Stdin = bytes.NewReader(ev1527(0xA5C3F1, rate, 100e3, 6))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rtl_433: %v", err)
	}
	// PWM: long pulse = 0 bit in rtl_433's convention, so the 24 code bits
	// arrive inverted; 0xA5C3F1 -> 0x5A3C0E, then the sync pulse adds a 25th bit.
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		t.Logf("decoded: %s", line)
		if obj["model"] == "sweep-test" && strings.Contains(line, "5a3c0e") {
			found = true
		}
	}
	if !found {
		t.Fatalf("rtl_433 did not decode the test remote; stdout: %q", out)
	}
}
