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
	"os/signal"
	"strconv"
	"strings"
	"time"
)

func init() {
	commands["transcripts"] = command{"transcripts [-q text] [-freq 122.9M] [-limit 100] [-follow]", cmdTranscripts}
	commands["transcribe"] = command{"transcribe <seconds> | transcribe <path of a WAV in the data dir> | transcribe on|off | transcribe status", cmdTranscribe}
}

type transcript struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	FreqHz    float64 `json:"freq_hz"`
	Label     string  `json:"label"`
	Mode      string  `json:"mode"`
	Start     float64 `json:"start"`
	DurationS float64 `json:"duration_s"`
	Text      string  `json:"text"`
	Empty     bool    `json:"empty"`
	URL       string  `json:"url"`
}

func (t transcript) line() string {
	s := fmt.Sprintf("%s  %.4f MHz %-3s %4.1f s", time.UnixMilli(int64(t.Start*1e3)).Format("15:04:05"), t.FreqHz/1e6, strings.ToUpper(t.Mode), t.DurationS)
	if t.Label != "" {
		s += "  " + t.Label
	}
	if t.Empty {
		return s + "  (no speech)"
	}
	return s + "  " + t.Text
}

func cmdTranscripts(a []string) error {
	fs := flag.NewFlagSet("transcripts", flag.ContinueOnError)
	q := fs.String("q", "", "full-text filter")
	freq := fs.String("freq", "", "only this frequency, e.g. 122.9M")
	limit := fs.Int("limit", 100, "at most this many, newest first")
	follow := fs.Bool("follow", false, "stay in the foreground and print a line per new transcript")
	if err := fs.Parse(a); err != nil {
		return err
	}
	v := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *q != "" {
		v.Set("q", *q)
	}
	if *freq != "" {
		hz, err := ParseFreq(*freq)
		if err != nil {
			return err
		}
		v.Set("freq_hz", strconv.FormatUint(hz, 10))
	}
	if !*follow {
		return get("/api/transcripts", v)
	}
	var last int64
	if raw, err := getBody("/api/events", url.Values{"limit": {"1000"}}); err == nil {
		var evs []struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(raw, &evs) == nil && len(evs) > 0 {
			last = evs[len(evs)-1].ID
		}
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	for {
		raw, err := getBody("/api/events", url.Values{"since": {strconv.FormatInt(last, 10)}, "limit": {"1000"}})
		if err != nil {
			return err
		}
		var evs []struct {
			ID      int64      `json:"id"`
			Decoder string     `json:"decoder"`
			Data    transcript `json:"data"`
		}
		if err := json.Unmarshal(raw, &evs); err != nil {
			return err
		}
		for _, e := range evs {
			last = e.ID
			if e.Decoder != "transcribe" {
				continue
			}
			t := e.Data
			if (*q != "" && !strings.Contains(strings.ToLower(t.Text), strings.ToLower(*q))) ||
				(v.Get("freq_hz") != "" && v.Get("freq_hz") != strconv.FormatInt(int64(t.FreqHz), 10)) {
				continue
			}
			fmt.Println(t.line())
		}
		select {
		case <-sig:
			return nil
		case <-time.After(700 * time.Millisecond):
		}
	}
}

func cmdTranscribe(a []string) error {
	if len(a) != 1 {
		return usageErr("transcribe")
	}
	switch a[0] {
	case "status":
		return get("/api/transcribe", nil)
	case "on", "off":
		return put("/api/transcribe", map[string]bool{"enabled": a[0] == "on"})
	}
	body := map[string]any{}
	if s, err := strconv.ParseFloat(a[0], 64); err == nil {
		body["seconds"] = s
	} else {
		body["path"] = a[0]
	}
	// recording and transcribing take longer than the default 10 s timeout
	b, _ := json.Marshal(body)
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Post("http://"+addr+"/api/transcribe", "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("sdrd not reachable at %s: %w", addr, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var buf bytes.Buffer
	if json.Indent(&buf, raw, "", "  ") == nil {
		raw = buf.Bytes()
	}
	if resp.StatusCode >= 300 {
		os.Stderr.Write(raw)
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	os.Stdout.Write(raw)
	fmt.Println()
	return nil
}
