package meta

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"
)

// faaRec is what is kept per aircraft. Owner names and addresses are never
// read into memory: only fields 0 (N-number), 2 (MFR MDL CODE), 4 (YEAR MFR)
// and the last data field (MODE S CODE HEX) of MASTER.txt are looked at.
type faaRec struct {
	reg  [6]byte // N-number without the N, NUL padded
	typ  uint16  // index into faaIndex.types, 0 = unknown
	year uint16
}

type faaIndex struct {
	recs  map[uint32]faaRec
	types []string // interned "MANUFACTURER MODEL" strings
}

func (f *faaIndex) lookup(hex uint32) (reg, typ string, year int, ok bool) {
	r, ok := f.recs[hex]
	if !ok {
		return
	}
	reg = "N" + strings.TrimRight(string(r.reg[:]), "\x00")
	if int(r.typ) < len(f.types) {
		typ = f.types[r.typ]
	}
	return reg, typ, int(r.year), true
}

// parseACFTREF reads ACFTREF.txt (CODE,MFR,MODEL,...) into code -> "MFR MODEL".
func parseACFTREF(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			continue
		}
		f := strings.SplitN(line, ",", 4)
		if len(f) < 3 {
			continue
		}
		code := strings.TrimSpace(strings.TrimPrefix(f[0], "\xef\xbb\xbf"))
		name := strings.TrimSpace(strings.TrimSpace(f[1]) + " " + strings.TrimSpace(f[2]))
		if code != "" && name != "" {
			out[code] = name
		}
	}
	return out
}

// parseMaster reads MASTER.txt keeping only registration, type code, year and hex.
func parseMaster(r io.Reader, refs map[string]string) (*faaIndex, error) {
	idx := &faaIndex{recs: make(map[uint32]faaRec, 320000), types: []string{""}}
	typeIdx := map[string]uint16{}
	br := bufio.NewReaderSize(r, 256*1024)
	first := true
	for {
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull { // absurdly long line: skip the rest of it
			for err == bufio.ErrBufferFull {
				_, err = br.ReadSlice('\n')
			}
			continue
		}
		if len(line) > 0 {
			if first {
				first = false
			} else {
				addMasterLine(idx, typeIdx, refs, line)
			}
		}
		if err != nil {
			if err == io.EOF {
				return idx, nil
			}
			return nil, err
		}
	}
}

func addMasterLine(idx *faaIndex, typeIdx map[string]uint16, refs map[string]string, line []byte) {
	line = bytes.TrimRight(line, "\r\n")
	if n := len(line); n > 0 && line[n-1] == ',' { // every row ends with a comma
		line = line[:n-1]
	}
	// leading fields: 0 N-number, 1 serial, 2 MFR MDL CODE, 3 ENG MFR MDL, 4 YEAR MFR
	var f [5][]byte
	rest := line
	for i := range f {
		j := bytes.IndexByte(rest, ',')
		if j < 0 {
			return
		}
		f[i], rest = rest[:j], rest[j+1:]
	}
	// the hex is the last data field (the line ends with a trailing comma, trimmed above)
	hexField := rest
	if j := bytes.LastIndexByte(rest, ','); j >= 0 {
		hexField = rest[j+1:]
	}
	hexField = bytes.TrimSpace(hexField)
	hv, err := strconv.ParseUint(string(hexField), 16, 32)
	if err != nil || len(hexField) != 6 {
		return
	}
	reg := bytes.TrimSpace(f[0])
	if len(reg) == 0 || len(reg) > 6 {
		return
	}
	var rec faaRec
	copy(rec.reg[:], reg)
	if y, err := strconv.Atoi(string(bytes.TrimSpace(f[4]))); err == nil && y > 1900 && y < 3000 {
		rec.year = uint16(y)
	}
	if name, ok := refs[string(bytes.TrimSpace(f[2]))]; ok {
		ti, ok := typeIdx[name]
		if !ok && len(idx.types) < 65535 {
			ti = uint16(len(idx.types))
			idx.types = append(idx.types, name)
			typeIdx[name] = ti
		}
		rec.typ = ti
	}
	idx.recs[uint32(hv)] = rec
}
