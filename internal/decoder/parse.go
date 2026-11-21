package decoder

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- APRS (TNC2 monitor format from direwolf) ----

var hexEsc = regexp.MustCompile(`<0x[0-9a-fA-F]{2}>`)

// parseAPRS parses "SRC>DST,PATH:info". Uncompressed position reports also
// yield lat/lon/comment.
func parseAPRS(s string) (map[string]any, bool) {
	s = strings.TrimSpace(hexEsc.ReplaceAllString(s, ""))
	head, info, ok := strings.Cut(s, ":")
	if !ok {
		return nil, false
	}
	from, rest, ok := strings.Cut(head, ">")
	if !ok || from == "" || strings.ContainsAny(from, " ") {
		return nil, false
	}
	parts := strings.Split(rest, ",")
	out := map[string]any{"raw": s, "from": from, "to": parts[0], "path": parts[1:], "info": info}
	if lat, lon, comment, ok := aprsPosition(info); ok {
		out["lat"], out["lon"], out["comment"] = lat, lon, comment
	}
	return out, true
}

// aprsPosition decodes an uncompressed position: !, =, or /, @ with a 7-char timestamp.
func aprsPosition(info string) (lat, lon float64, comment string, ok bool) {
	if info == "" {
		return
	}
	p := info[1:]
	switch info[0] {
	case '!', '=':
	case '/', '@':
		if len(p) < 7 {
			return
		}
		p = p[7:]
	default:
		return
	}
	// DDMM.mmN S DDDMM.mmW C
	if len(p) < 19 {
		return
	}
	la, err1 := dm(p[0:7], 2)
	lo, err2 := dm(p[9:17], 3)
	if err1 != nil || err2 != nil {
		return
	}
	if p[7] == 'S' {
		la = -la
	} else if p[7] != 'N' {
		return
	}
	if p[17] == 'W' {
		lo = -lo
	} else if p[17] != 'E' {
		return
	}
	return round6(la), round6(lo), strings.TrimSpace(p[19:]), true
}

func dm(s string, degDigits int) (float64, error) {
	s = strings.ReplaceAll(s, " ", "0") // position ambiguity
	d, err := strconv.Atoi(s[:degDigits])
	if err != nil {
		return 0, err
	}
	mins, err := strconv.ParseFloat(s[degDigits:], 64)
	if err != nil {
		return 0, err
	}
	return float64(d) + mins/60, nil
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// ---- ADS-B (BaseStation / SBS-1 from dump1090) ----

type aircraft struct {
	Hex      string   `json:"hex"`
	Flight   string   `json:"flight,omitempty"`
	AltBaro  *int     `json:"alt_baro,omitempty"`
	GS       *float64 `json:"gs,omitempty"`
	Track    *float64 `json:"track,omitempty"`
	Lat      *float64 `json:"lat,omitempty"`
	Lon      *float64 `json:"lon,omitempty"`
	Squawk   string   `json:"squawk,omitempty"`
	Seen     float64  `json:"seen"`
	Messages int      `json:"messages"`
	last     time.Time
}

type aircraftTable struct{ m map[string]*aircraft }

func newAircraftTable() *aircraftTable { return &aircraftTable{m: map[string]*aircraft{}} }

// applySBS applies one "MSG,..." line: 4 hex, 10 callsign, 11 alt, 12 gs,
// 13 track, 14 lat, 15 lon, 17 squawk.
func (t *aircraftTable) applySBS(line string, now time.Time) {
	f := strings.Split(line, ",")
	if len(f) < 18 || f[0] != "MSG" || f[4] == "" {
		return
	}
	hex := strings.ToLower(f[4])
	a := t.m[hex]
	if a == nil {
		a = &aircraft{Hex: hex}
		t.m[hex] = a
	}
	a.Messages++
	a.last = now
	if v := strings.TrimSpace(f[10]); v != "" {
		a.Flight = v
	}
	if v, err := strconv.Atoi(f[11]); err == nil {
		a.AltBaro = &v
	}
	if v, err := strconv.ParseFloat(f[12], 64); err == nil {
		a.GS = &v
	}
	if v, err := strconv.ParseFloat(f[13], 64); err == nil {
		a.Track = &v
	}
	if la, err := strconv.ParseFloat(f[14], 64); err == nil {
		if lo, err := strconv.ParseFloat(f[15], 64); err == nil {
			a.Lat, a.Lon = &la, &lo
		}
	}
	if v := strings.TrimSpace(f[17]); v != "" {
		a.Squawk = v
	}
}

// snapshot returns aircraft seen in the last 60 s, most recent first.
func (t *aircraftTable) snapshot(now time.Time) []aircraft {
	out := []aircraft{}
	for k, a := range t.m {
		age := now.Sub(a.last).Seconds()
		if age > 60 {
			delete(t.m, k)
			continue
		}
		c := *a
		c.Seen = math.Round(age*10) / 10
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seen < out[j].Seen })
	return out
}

// ---- AIS (NMEA !AIVDM) ----

type aisAssembler struct {
	parts map[string][]string // key: seq id + channel
}

func newAISAssembler() *aisAssembler { return &aisAssembler{parts: map[string][]string{}} }

// add takes one !AIVDM sentence and returns the decoded message once complete.
func (a *aisAssembler) add(sentence string) (map[string]any, bool) {
	body, _, _ := strings.Cut(sentence, "*")
	f := strings.Split(body, ",")
	if len(f) < 7 {
		return nil, false
	}
	total, _ := strconv.Atoi(f[1])
	num, _ := strconv.Atoi(f[2])
	key := f[3] + f[4]
	payload := f[5]
	fill, _ := strconv.Atoi(f[6])
	if total > 1 {
		if num == 1 {
			a.parts[key] = nil
		}
		a.parts[key] = append(a.parts[key], payload)
		if num < total || len(a.parts[key]) != total {
			return nil, false
		}
		payload = strings.Join(a.parts[key], "")
		delete(a.parts, key)
	}
	msg := decodeAIS(payload, fill)
	if msg == nil {
		return nil, false
	}
	msg["nmea"] = sentence
	return msg, true
}

type bits []byte

func unarmor(payload string, fill int) bits {
	var b bits
	for _, c := range payload {
		v := int(c) - 48
		if v > 40 {
			v -= 8
		}
		for i := 5; i >= 0; i-- {
			b = append(b, byte(v>>i&1))
		}
	}
	if fill > 0 && fill <= len(b) {
		b = b[:len(b)-fill]
	}
	return b
}

func (b bits) u(start, n int) uint64 {
	var v uint64
	for i := start; i < start+n && i < len(b); i++ {
		v = v<<1 | uint64(b[i])
	}
	return v
}

func (b bits) s(start, n int) int64 {
	v := int64(b.u(start, n))
	if n > 0 && v>>(n-1)&1 == 1 {
		v -= 1 << n
	}
	return v
}

func (b bits) text(start, n int) string {
	var sb strings.Builder
	for i := 0; i+6 <= n && start+i+6 <= len(b); i += 6 {
		c := byte(b.u(start+i, 6))
		if c < 32 {
			c += 64
		}
		sb.WriteByte(c)
	}
	return strings.TrimRight(strings.TrimSpace(sb.String()), "@ ")
}

// decodeAIS decodes message types 1-3, 5, 18 and 24 (others: type + mmsi only).
func decodeAIS(payload string, fill int) map[string]any {
	b := unarmor(payload, fill)
	if len(b) < 38 {
		return nil
	}
	typ := int(b.u(0, 6))
	out := map[string]any{"type": typ, "mmsi": b.u(8, 30)}
	pos := func(lonStart, latStart int) {
		lon := float64(b.s(lonStart, 28)) / 600000
		lat := float64(b.s(latStart, 27)) / 600000
		if math.Abs(lon) <= 180 && math.Abs(lat) <= 90 {
			out["lon"], out["lat"] = round6(lon), round6(lat)
		}
	}
	switch typ {
	case 1, 2, 3:
		if len(b) < 137 {
			return out
		}
		out["status"] = b.u(38, 4)
		if sog := b.u(50, 10); sog != 1023 {
			out["sog"] = float64(sog) / 10
		}
		pos(61, 89)
		if cog := b.u(116, 12); cog < 3600 {
			out["cog"] = float64(cog) / 10
		}
		if h := b.u(128, 9); h != 511 {
			out["heading"] = h
		}
	case 18:
		if len(b) < 133 {
			return out
		}
		if sog := b.u(46, 10); sog != 1023 {
			out["sog"] = float64(sog) / 10
		}
		pos(57, 85)
		if cog := b.u(112, 12); cog < 3600 {
			out["cog"] = float64(cog) / 10
		}
		if h := b.u(124, 9); h != 511 {
			out["heading"] = h
		}
	case 5:
		if len(b) < 240 {
			return out
		}
		out["callsign"] = b.text(70, 42)
		out["name"] = b.text(112, 120)
		out["shiptype"] = b.u(232, 8)
	case 24:
		if b.u(38, 2) == 0 {
			out["name"] = b.text(40, 120)
		} else if len(b) >= 132 {
			out["shiptype"] = b.u(40, 8)
			out["callsign"] = b.text(90, 42)
		}
	}
	return out
}
