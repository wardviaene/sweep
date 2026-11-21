// sdrctl drives sdrd from the command line. All output is JSON so agents can parse it.
//
//	sdrctl status
//	sdrctl tune 446.0M [-rate 2.048M] [-gain auto|29.7]
//	sdrctl gain auto|29.7
//	sdrctl rate 2.4M
//	sdrctl peaks [-n 10] [-min-snr 10] [-frames 20] [-agg max|avg]
//	sdrctl spectrum [-bins 128] [-frames 20] [-agg avg|max]
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
