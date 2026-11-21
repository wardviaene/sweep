package rtltcp

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestHeaderAndCommands(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()

	go func() {
		hdr := []byte("RTL0\x00\x00\x00\x05\x00\x00\x00\x1d")
		srv.Write(hdr)
	}()
	c, err := newClient(cli)
	if err != nil {
		t.Fatal(err)
	}
	if c.Header.TunerName() != "R820T" || c.Header.GainCount != 29 {
		t.Fatalf("header = %+v", c.Header)
	}

	got := make(chan []byte, 1)
	go func() {
		b := make([]byte, 15)
		io.ReadFull(srv, b)
		got <- b
	}()
	g := 20.7
	if err := c.SetFreq(433920000); err != nil {
		t.Fatal(err)
	}
	if err := c.SetGain(&g); err != nil {
		t.Fatal(err)
	}
	b := <-got
	if b[0] != CmdFreq || binary.BigEndian.Uint32(b[1:5]) != 433920000 {
		t.Fatalf("freq cmd = %x", b[:5])
	}
	if b[5] != CmdGainMode || binary.BigEndian.Uint32(b[6:10]) != 1 {
		t.Fatalf("gain mode cmd = %x", b[5:10])
	}
	if b[10] != CmdGain || binary.BigEndian.Uint32(b[11:15]) != 207 {
		t.Fatalf("gain cmd = %x", b[10:15])
	}
}
