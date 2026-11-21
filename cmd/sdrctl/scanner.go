package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

func init() {
	commands["scanner"] = command{"scanner <band> | <lo> <hi> [-mode am|nfm|usb|lsb] [-bw 8k] [-step 25k] [-margin 10] [-hang 2] [-dwell 1500] [-max-tx 120] [-lockout f,f] [-priority f,f] [-follow] | scanner status | scanner stop | scanner lockout <freq> [-remove]", cmdScanner}
}

// freqList parses "132.15M,121.5M" into Hz values.
func freqList(s string) ([]uint64, error) {
	var out []uint64
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		hz, err := ParseFreq(f)
		if err != nil {
			return nil, err
		}
		out = append(out, hz)
	}
	return out, nil
}

func cmdScanner(a []string) error {
	if len(a) == 0 {
		return usageErr("scanner")
	}
	switch a[0] {
	case "status":
		return get("/api/scanner", nil)
	case "stop":
		return post("/api/scanner/stop", nil)
	case "lockout":
		if len(a) < 2 || strings.HasPrefix(a[1], "-") {
			return usageErr("scanner")
		}
		f, err := ParseFreq(a[1])
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("scanner lockout", flag.ContinueOnError)
		remove := fs.Bool("remove", false, "remove the lockout again")
		if err := fs.Parse(a[2:]); err != nil {
			return err
		}
		body := map[string]any{"freq_hz": f}
		if *remove {
			body["remove"] = true
		}
		return post("/api/scanner/lockout", body)
	}

	var pos []string
	for len(a) > 0 && !strings.HasPrefix(a[0], "-") {
		pos, a = append(pos, a[0]), a[1:]
	}
	fs := flag.NewFlagSet("scanner", flag.ContinueOnError)
	mode := fs.String("mode", "", "am, nfm, usb or lsb (default: the band's)")
	bw := fs.String("bw", "", "channel bandwidth, e.g. 8k (default per mode)")
	step := fs.String("step", "", "channel grid, e.g. 25k (default per mode)")
	margin := fs.Float64("margin", 0, "dB above the baseline that counts as a new carrier (default 10)")
	hang := fs.Float64("hang", 0, "seconds of quiet that end a transmission (default 2)")
	dwell := fs.Int("dwell", 0, "ms per window while idle (default 1500)")
	maxTx := fs.Float64("max-tx", 0, "seconds after which a carrier counts as continuous (default 120)")
	lockout := fs.String("lockout", "", "comma separated channels to ignore")
	priority := fs.String("priority", "", "comma separated channels that interrupt others")
	region := fs.String("region", "", "us or eu band edges")
	follow := fs.Bool("follow", false, "stay in the foreground and print a line per transmission; Ctrl-C stops the scanner")
	if err := fs.Parse(a); err != nil {
		return err
	}
	body := map[string]any{}
	switch len(pos) {
	case 1:
		body["band"] = pos[0]
	case 2:
		lo, err := ParseFreq(pos[0])
		if err != nil {
			return err
		}
		hi, err := ParseFreq(pos[1])
		if err != nil {
			return err
		}
		body["lo_hz"], body["hi_hz"] = lo, hi
	default:
		return usageErr("scanner")
	}
	for k, v := range map[string]string{"mode": *mode, "region": *region} {
		if v != "" {
			body[k] = v
		}
	}
	for k, v := range map[string]string{"bandwidth_hz": *bw, "step_hz": *step} {
		if v != "" {
			hz, err := ParseFreq(v)
			if err != nil {
				return err
			}
			body[k] = hz
		}
	}
	for k, v := range map[string]float64{"margin_db": *margin, "hang_s": *hang, "max_tx_s": *maxTx, "dwell_ms": float64(*dwell)} {
		if v != 0 {
			body[k] = v
		}
	}
	for k, v := range map[string]string{"lockout_hz": *lockout, "priority_hz": *priority} {
		if v != "" {
			l, err := freqList(v)
			if err != nil {
				return err
			}
			body[k] = l
		}
	}
	if !*follow {
		return post("/api/scanner", body)
	}

	// Remember where the event log is, so only new transmissions are printed.
	var last int64
	if raw, err := getBody("/api/events", url.Values{"limit": {"1000"}}); err == nil {
		var evs []struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(raw, &evs) == nil && len(evs) > 0 {
			last = evs[len(evs)-1].ID
		}
	}
	b, _ := json.Marshal(body)
	resp, err := client.Post("http://"+addr+"/api/scanner", "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("sdrd not reachable at %s: %w", addr, err)
	}
	if resp.StatusCode != http.StatusAccepted {
		return output(resp)
	}
	resp.Body.Close()
	fmt.Fprintln(os.Stderr, "scanner running; Ctrl-C stops it")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	for {
		select {
		case <-sig:
			fmt.Fprintln(os.Stderr, "stopping scanner...")
			c := &http.Client{Timeout: 30 * time.Second}
			resp, err := c.Post("http://"+addr+"/api/scanner/stop", "application/json", nil)
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		case <-time.After(500 * time.Millisecond):
		}
		raw, err := getBody("/api/events", url.Values{"since": {strconv.FormatInt(last, 10)}, "limit": {"1000"}})
		if err != nil {
			return err
		}
		var evs []struct {
			ID      int64   `json:"id"`
			Decoder string  `json:"decoder"`
			TS      float64 `json:"ts"`
			Data    struct {
				FreqHz    float64 `json:"freq_hz"`
				Label     string  `json:"label"`
				Start     float64 `json:"start"`
				DurationS float64 `json:"duration_s"`
				PeakDB    float64 `json:"peak_db"`
				Mode      string  `json:"mode"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &evs); err != nil {
			return err
		}
		for _, e := range evs {
			last = e.ID
			if e.Decoder != "scanner" {
				continue
			}
			d := e.Data
			line := fmt.Sprintf("%s  %.4f MHz %-3s %5.1f s  %5.1f dB", time.UnixMilli(int64(d.Start*1e3)).Format("15:04:05"),
				d.FreqHz/1e6, strings.ToUpper(d.Mode), d.DurationS, d.PeakDB)
			if d.Label != "" {
				line += "  " + d.Label
			}
			fmt.Println(line)
		}
		// stop following when the scanner was stopped by someone else
		if raw, err := getBody("/api/scanner", nil); err == nil {
			var st struct {
				State string `json:"state"`
			}
			if json.Unmarshal(raw, &st) == nil && st.State == "stopped" {
				return nil
			}
		}
	}
}
