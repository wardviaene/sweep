package decoder

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParseDSDLine(t *testing.T) {
	ev, ok := parseDSDLine("\x1b[32mSync: +DMR  [slot1]  slot2  | Color Code=01 | VC3 \x1b[0m TGT=3100 SRC=3125487 ")
	if !ok || ev["mode"] != "DMR" || ev["tgt"] != 3100 || ev["src"] != 3125487 || ev["color_code"] != 1 {
		t.Fatalf("%v", ev)
	}
	ev, ok = parseDSDLine("  Source: 00001234; Target: 00000009; ")
	if !ok || ev["tgt"] != 9 || ev["src"] != 1234 {
		t.Fatalf("%v", ev)
	}
	if _, ok := parseDSDLine("Audio input: stdin"); ok {
		t.Fatal("noise parsed")
	}
	if _, ok := parseDSDLine("14:10:16 Sync: -P25p2  P2 Missing Parameters"); ok {
		t.Fatal("sync without call metadata counted as a call")
	}
}

func TestReadFISB(t *testing.T) {
	ev := NewEvents()
	m := &Manager{ev: ev}
	text := `UPLINK:
 Site Latitude:     +12.0000
INFORMATION FRAME:
 Type:              0 (FIS-B APDU)
FIS-B:
 Product ID:        413 (Generic Textual Data Product APDU Payload Format Type 2) - Text (DLAC)
 Product time:      02:06
 Report type:       METAR
 Report location:   KXYZ
 Report time:       211953Z
 Text:
METAR KXYZ 211953Z 18012KT 10SM FEW040 24/12 A3001 RMK  
     AO2 SLP160=

INFORMATION FRAME:
`
	m.readFISB(strings.NewReader(text))
	got := ev.Since(0, 10)
	if len(got) != 1 {
		t.Fatalf("events %+v", got)
	}
	d := got[0].Data.(map[string]any)
	if d["report_type"] != "METAR" || d["location"] != "KXYZ" || !strings.HasPrefix(d["text"].(string), "METAR KXYZ") || !strings.HasSuffix(d["text"].(string), "AO2 SLP160=") {
		t.Fatalf("%v", d)
	}
}

func TestNormUATAircraft(t *testing.T) {
	a := normUATAircraft(map[string]any{"hex": "~A1B2C3", "flight": "N123  ", "altitude": 4500.0, "speed": 110.0})
	if a["hex"] != "a1b2c3" || a["flight"] != "N123" || a["alt_baro"] != 4500.0 || a["gs"] != 110.0 {
		t.Fatalf("%v", a)
	}
}

// rs41mod must accept our streaming WAV header and run on noise without errors.
func TestRadiosondeDecoderAcceptsStream(t *testing.T) {
	toolDirs = []string{defaultToolDirForTest()}
	bin, err := lookTool("rs41mod")
	if err != nil {
		t.Skip("rs41mod not installed")
	}
	var in bytes.Buffer
	in.Write(wavHeader(48000))
	for i := 0; i < 48000; i++ { // 1 s of low noise
		binary.Write(&in, binary.LittleEndian, int16((i*7919)%200-100))
	}
	cmd := exec.Command(bin, "--ecc2", "--ptu", "--json")
	cmd.Stdin = &in
	var errb bytes.Buffer
	cmd.Stderr = &errb
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		if err != nil || strings.Contains(strings.ToLower(errb.String()), "error") {
			t.Fatalf("rs41mod: %v %s", err, errb.String())
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("rs41mod did not finish on 1 s of input")
	}
}

func defaultToolDirForTest() string {
	home, _ := os.UserHomeDir()
	return home + "/.local/share/sweep/bin"
}

func TestAmateurBand(t *testing.T) {
	for f, want := range map[uint32]bool{146_520_000: true, 446_000_000: true, 433_920_000: true, 460_000_000: false, 851_000_000: false, 162_550_000: false} {
		if amateurBand(f) != want {
			t.Errorf("%d: got %v", f, !want)
		}
	}
}
