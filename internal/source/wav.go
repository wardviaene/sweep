package source

import (
	"encoding/binary"
	"fmt"
	"os"
)

// ReadWAV reads a 16-bit PCM WAV file and returns its first channel and rate.
func ReadWAV(path string) ([]int16, float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("%s: not a WAV file", path)
	}
	var rate, channels, bitsPer int
	for p := 12; p+8 <= len(b); {
		id, n := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:p+8]))
		body := b[p+8 : min(p+8+n, len(b))]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, 0, fmt.Errorf("%s: short fmt chunk", path)
			}
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bitsPer = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			if bitsPer != 16 || channels < 1 {
				return nil, 0, fmt.Errorf("%s: need 16-bit PCM, got %d-bit %d ch", path, bitsPer, channels)
			}
			frames := len(body) / (2 * channels)
			out := make([]int16, frames)
			for i := range out {
				out[i] = int16(binary.LittleEndian.Uint16(body[2*i*channels:]))
			}
			return out, float64(rate), nil
		}
		p += 8 + n + n%2
	}
	return nil, 0, fmt.Errorf("%s: no data chunk", path)
}
