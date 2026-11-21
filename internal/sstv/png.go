package sstv

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
)

// WritePNG writes img to path atomically (temporary file, then rename), so
// readers never see a half-written picture.
func WritePNG(path string, img image.Image) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".sstv-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err = enc.Encode(f, img)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
