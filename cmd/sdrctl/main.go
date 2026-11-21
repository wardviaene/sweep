// sdrctl drives sdrd from the command line. All output is JSON so agents can parse it.
//
//	sdrctl status
//	sdrctl tune 446.0M [-rate 2.048M] [-gain auto|29.7]
//	sdrctl gain auto|29.7
//	sdrctl rate 2.4M
//	sdrctl peaks [-n 10] [-min-snr 10] [-frames 20] [-agg max|avg]
//	sdrctl spectrum [-bins 128] [-frames 20] [-agg avg|max]
//	sdrctl listen 446.00625M [-mode nfm] [-bw 12.5k] [-squelch -60|off]
//	sdrctl demod off|nfm|am|usb|lsb
//	sdrctl decoder rtl_433|adsb|aprs|ais|none [-region us|eu]
//	sdrctl events [-since 0] [-limit 50] [-decoder rtl_433]
//	sdrctl bands [-region us|eu]
//	sdrctl band <id> [-region us|eu]
//	sdrctl scan 430M 440M [-dwell 500] [-min-snr 10]
//	sdrctl record <seconds>
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var addr = envOr("SDRD_ADDR", "127.0.0.1:8073")

type command struct {
	usage string
	run   func(args []string) error
}

var commands = map[string]command{}

func init() {
	commands["status"] = command{"status", func([]string) error { return get("/api/status", nil) }}
	commands["tune"] = command{"tune <freq> [-rate sps] [-gain auto|dB]", cmdTune}
	commands["gain"] = command{"gain <auto|dB>", func(a []string) error {
		if len(a) != 1 {
			return usageErr("gain")
		}
		body := map[string]any{}
		if err := setGain(body, a[0]); err != nil {
			return err
		}
		return post("/api/tune", body)
	}}
	commands["rate"] = command{"rate <sps>", func(a []string) error {
		if len(a) != 1 {
			return usageErr("rate")
		}
		r, err := ParseFreq(a[0])
		if err != nil {
			return err
		}
		return post("/api/tune", map[string]any{"sample_rate": r})
	}}
	commands["peaks"] = command{"peaks [-n 10] [-min-snr 10] [-frames 20] [-agg max|avg]", func(a []string) error {
		fs := flag.NewFlagSet("peaks", flag.ContinueOnError)
		n := fs.Int("n", 10, "max peaks")
		snr := fs.Float64("min-snr", 10, "minimum dB above the median floor")
		frames := fs.Int("frames", 20, "recent frames to aggregate (20 = 1 s)")
		agg := fs.String("agg", "max", "max (catches bursts) or avg")
		if err := fs.Parse(a); err != nil {
			return err
		}
		return get("/api/peaks", url.Values{"n": {strconv.Itoa(*n)}, "min_snr": {fmt.Sprint(*snr)},
			"frames": {strconv.Itoa(*frames)}, "agg": {*agg}})
	}}
	commands["spectrum"] = command{"spectrum [-bins 128] [-frames 20] [-agg avg|max]", func(a []string) error {
		fs := flag.NewFlagSet("spectrum", flag.ContinueOnError)
		bins := fs.Int("bins", 128, "output bins (max-pooled)")
		frames := fs.Int("frames", 20, "recent frames to aggregate")
		agg := fs.String("agg", "avg", "avg or max")
		if err := fs.Parse(a); err != nil {
			return err
		}
		return get("/api/spectrum", url.Values{"bins": {strconv.Itoa(*bins)}, "frames": {strconv.Itoa(*frames)}, "agg": {*agg}})
	}}
}

func init() {
	commands["listen"] = command{"listen <freq> [-mode nfm|am|usb|lsb] [-bw 12.5k] [-squelch dB|off]", cmdListen}
	commands["demod"] = command{"demod <off|nfm|am|usb|lsb>", func(a []string) error {
		if len(a) != 1 {
			return usageErr("demod")
		}
		return post("/api/demod", map[string]any{"mode": a[0]})
	}}
	commands["decoder"] = command{"decoder <rtl_433|adsb|aprs|ais|none> [-region us|eu]", func(a []string) error {
		if len(a) < 1 || strings.HasPrefix(a[0], "-") {
			return usageErr("decoder")
		}
		fs := flag.NewFlagSet("decoder", flag.ContinueOnError)
		region := fs.String("region", "us", "us or eu (APRS frequency)")
		if err := fs.Parse(a[1:]); err != nil {
			return err
		}
		return post("/api/decoder", map[string]any{"decoder": a[0], "region": *region})
	}}
	commands["events"] = command{"events [-since 0] [-limit 50] [-decoder name]", cmdEvents}
	commands["bands"] = command{"bands [-region us|eu]", cmdBands}
	commands["band"] = command{"band <id> [-region us|eu]   (tune to a band; listen if it has a voice mode)", cmdBand}
	commands["scan"] = command{"scan <start> <stop> [-dwell 500] [-min-snr 10] [-max 50]", func(a []string) error {
		if len(a) < 2 {
			return usageErr("scan")
		}
		lo, err := ParseFreq(a[0])
		if err != nil {
			return err
		}
		hi, err := ParseFreq(a[1])
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("scan", flag.ContinueOnError)
		dwell := fs.Int("dwell", 500, "ms per step")
		snr := fs.Float64("min-snr", 10, "dB above floor")
		mx := fs.Int("max", 50, "max peaks")
		if err := fs.Parse(a[2:]); err != nil {
			return err
		}
		client.Timeout = 10 * time.Minute
		return post("/api/scan", map[string]any{"start_hz": lo, "stop_hz": hi, "dwell_ms": *dwell, "min_snr": *snr, "max": *mx})
	}}
	commands["record"] = command{"record <seconds>   (writes a cu8 file next to sdrd; play back with sdrd -iq FILE)", func(a []string) error {
		if len(a) != 1 {
			return usageErr("record")
		}
		secs, err := strconv.ParseFloat(a[0], 64)
		if err != nil {
			return err
		}
		client.Timeout = time.Duration(secs+30) * time.Second
		return post("/api/record", map[string]any{"seconds": secs})
	}}
}

func cmdListen(a []string) error {
	if len(a) < 1 || strings.HasPrefix(a[0], "-") {
		return usageErr("listen")
	}
	f, err := ParseFreq(a[0])
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("listen", flag.ContinueOnError)
	mode := fs.String("mode", "nfm", "nfm, am, usb or lsb")
	bw := fs.String("bw", "", "channel bandwidth, e.g. 12.5k (default per mode)")
	sq := fs.String("squelch", "", "squelch in dB, or off")
	if err := fs.Parse(a[1:]); err != nil {
		return err
	}
	body := map[string]any{"mode": *mode, "freq_hz": f}
	if *bw != "" {
		b, err := ParseFreq(*bw)
		if err != nil {
			return err
		}
		body["bandwidth_hz"] = b
	}
	switch *sq {
	case "":
	case "off":
		body["squelch_db"] = nil
	default:
		v, err := strconv.ParseFloat(*sq, 64)
		if err != nil {
			return fmt.Errorf("squelch %q: want dB or off", *sq)
		}
		body["squelch_db"] = v
	}
	return post("/api/demod", body)
}

func cmdEvents(a []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	since := fs.Int64("since", 0, "only events with id > since")
	limit := fs.Int("limit", 50, "max events")
	dec := fs.String("decoder", "", "filter by decoder")
	if err := fs.Parse(a); err != nil {
		return err
	}
	b, err := getBody("/api/events", url.Values{"since": {strconv.FormatInt(*since, 10)}, "limit": {"1000"}})
	if err != nil {
		return err
	}
	var evs []map[string]any
	if err := json.Unmarshal(b, &evs); err != nil {
		return err
	}
	out := []map[string]any{}
	for _, e := range evs {
		if *dec == "" || e["decoder"] == *dec {
			out = append(out, e)
		}
	}
	if len(out) > *limit {
		out = out[len(out)-*limit:]
	}
	return printJSON(out)
}

type band struct {
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Regions  []string             `json:"regions"`
	Lo       float64              `json:"lo_mhz"`
	Hi       float64              `json:"hi_mhz"`
	Edges    map[string][]float64 `json:"edges,omitempty"`
	Tune     float64              `json:"tune_mhz"`
	HF       bool                 `json:"hf"`
	Mode     string               `json:"mode"`
	Decoder  *string              `json:"decoder"`
	OnAir    string               `json:"on_air"`
	Antenna  string               `json:"antenna"`
	Channels []struct {
		MHz     float64  `json:"mhz"`
		Name    string   `json:"name"`
		Mode    string   `json:"mode"`
		Regions []string `json:"regions,omitempty"`
	} `json:"channels"`
}

func loadBands(region string) ([]band, error) {
	b, err := getBody("/api/bands", nil)
	if err != nil {
		return nil, err
	}
	var cat struct {
		Bands []band `json:"bands"`
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		return nil, err
	}
	var out []band
	for _, bd := range cat.Bands {
		if !slicesContains(bd.Regions, region) {
			continue
		}
		if e, ok := bd.Edges[region]; ok {
			bd.Lo, bd.Hi = e[0], e[1]
		}
		bd.Edges = nil
		chs := bd.Channels[:0]
		for _, c := range bd.Channels {
			if len(c.Regions) == 0 || slicesContains(c.Regions, region) {
				c.Regions = nil
				chs = append(chs, c)
			}
		}
		bd.Channels = chs
		out = append(out, bd)
	}
	return out, nil
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func cmdBands(a []string) error {
	fs := flag.NewFlagSet("bands", flag.ContinueOnError)
	region := fs.String("region", "us", "us or eu")
	if err := fs.Parse(a); err != nil {
		return err
	}
	bs, err := loadBands(*region)
	if err != nil {
		return err
	}
	return printJSON(bs)
}

func cmdBand(a []string) error {
	if len(a) < 1 || strings.HasPrefix(a[0], "-") {
		return usageErr("band")
	}
	fs := flag.NewFlagSet("band", flag.ContinueOnError)
	region := fs.String("region", "us", "us or eu")
	if err := fs.Parse(a[1:]); err != nil {
		return err
	}
	bs, err := loadBands(*region)
	if err != nil {
		return err
	}
	for _, b := range bs {
		if b.ID != a[0] {
			continue
		}
		f := uint64(b.Tune*1e6 + 0.5)
		if b.Mode != "none" {
			return post("/api/demod", map[string]any{"mode": b.Mode, "freq_hz": f})
		}
		return post("/api/tune", map[string]any{"freq_hz": f})
	}
	return fmt.Errorf("no band %q in region %s (see sdrctl bands)", a[0], *region)
}

func getBody(path string, q url.Values) ([]byte, error) {
	u := "http://" + addr + path
	if q != nil {
		u += "?" + q.Encode()
	}
	resp, err := client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("sdrd not reachable at %s: %w", addr, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
	}
	return b, err
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func cmdTune(a []string) error {
	if len(a) < 1 || strings.HasPrefix(a[0], "-") {
		return usageErr("tune")
	}
	f, err := ParseFreq(a[0])
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("tune", flag.ContinueOnError)
	rate := fs.String("rate", "", "sample rate, e.g. 2.048M")
	gain := fs.String("gain", "", "auto or dB")
	if err := fs.Parse(a[1:]); err != nil {
		return err
	}
	body := map[string]any{"freq_hz": f}
	if *rate != "" {
		r, err := ParseFreq(*rate)
		if err != nil {
			return err
		}
		body["sample_rate"] = r
	}
	if *gain != "" {
		if err := setGain(body, *gain); err != nil {
			return err
		}
	}
	return post("/api/tune", body)
}

func setGain(body map[string]any, s string) error {
	if s == "auto" {
		body["gain_db"] = nil
		return nil
	}
	g, err := strconv.ParseFloat(strings.TrimSuffix(s, "dB"), 64)
	if err != nil {
		return fmt.Errorf("gain %q: want auto or a number of dB", s)
	}
	body["gain_db"] = g
	return nil
}

// ParseFreq parses 146.52M, 100k, 1.09G, 433920000 or 433.92e6 into Hz.
func ParseFreq(s string) (uint64, error) {
	mult := 1.0
	t := strings.TrimSuffix(strings.TrimSpace(s), "Hz")
	switch {
	case strings.HasSuffix(t, "G"), strings.HasSuffix(t, "g"):
		mult, t = 1e9, t[:len(t)-1]
	case strings.HasSuffix(t, "M"), strings.HasSuffix(t, "m"):
		mult, t = 1e6, t[:len(t)-1]
	case strings.HasSuffix(t, "k"), strings.HasSuffix(t, "K"):
		mult, t = 1e3, t[:len(t)-1]
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("bad frequency %q (examples: 146.52M, 100k, 433920000)", s)
	}
	return uint64(v*mult + 0.5), nil
}

var client = &http.Client{Timeout: 10 * time.Second}

func get(path string, q url.Values) error {
	u := "http://" + addr + path
	if q != nil {
		u += "?" + q.Encode()
	}
	resp, err := client.Get(u)
	if err != nil {
		return fmt.Errorf("sdrd not reachable at %s: %w", addr, err)
	}
	return output(resp)
}

func post(path string, body any) error {
	b, _ := json.Marshal(body)
	resp, err := client.Post("http://"+addr+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("sdrd not reachable at %s: %w", addr, err)
	}
	return output(resp)
}

func output(resp *http.Response) error {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if json.Indent(&buf, b, "", "  ") == nil {
		b = buf.Bytes()
	}
	if resp.StatusCode >= 300 {
		os.Stderr.Write(b)
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	os.Stdout.Write(b)
	if !bytes.HasSuffix(b, []byte("\n")) {
		fmt.Println()
	}
	return nil
}

func usageErr(name string) error { return fmt.Errorf("usage: sdrctl %s", commands[name].usage) }

func usage() {
	fmt.Fprintf(os.Stderr, "usage: sdrctl [-addr host:port] <command> [args]\n\nsdrd address defaults to $SDRD_ADDR or 127.0.0.1:8073.\n\ncommands:\n")
	for _, n := range sortedKeys() {
		fmt.Fprintf(os.Stderr, "  %s\n", commands[n].usage)
	}
}

func sortedKeys() []string {
	var ks []string
	for k := range commands {
		ks = append(ks, k)
	}
	for i := range ks {
		for j := i + 1; j < len(ks); j++ {
			if ks[j] < ks[i] {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	return ks
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	flag.StringVar(&addr, "addr", addr, "sdrd address")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}
	cmd, ok := commands[flag.Arg(0)]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n", flag.Arg(0))
		usage()
		os.Exit(2)
	}
	if err := cmd.run(flag.Args()[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sdrctl:", err)
		os.Exit(1)
	}
}
