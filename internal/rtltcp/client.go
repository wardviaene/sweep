// Package rtltcp is a client for the rtl_tcp IQ server protocol.
//
// On connect the server sends a 12-byte header: "RTL0", tuner type (uint32 BE),
// tuner gain count (uint32 BE). After that it streams interleaved unsigned
// 8-bit I/Q samples. Commands are 5 bytes: 1 byte command, uint32 BE parameter.
package rtltcp

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Command codes from rtl_tcp.c.
const (
	CmdFreq           = 0x01
	CmdSampleRate     = 0x02
	CmdGainMode       = 0x03 // 0 = auto, 1 = manual
	CmdGain           = 0x04 // tenths of a dB
	CmdFreqCorrection = 0x05 // ppm
	CmdAGCMode        = 0x08 // RTL2832 digital AGC
	CmdDirectSampling = 0x09 // 0 off, 1 I-branch, 2 Q-branch
	CmdOffsetTuning   = 0x0a
	CmdBiasTee        = 0x0e
)

// Tuner types as reported in the header (enum rtlsdr_tuner).
var tunerNames = map[uint32]string{
	0: "unknown", 1: "E4000", 2: "FC0012", 3: "FC0013", 4: "FC2580", 5: "R820T", 6: "R828D",
}

// Header is the dongle info sent by rtl_tcp on connect.
type Header struct {
	TunerType uint32
	GainCount uint32
}

// TunerName returns a readable tuner name.
func (h Header) TunerName() string {
	if n, ok := tunerNames[h.TunerType]; ok {
		return n
	}
	return fmt.Sprintf("tuner(%d)", h.TunerType)
}

// Client is a connection to rtl_tcp.
type Client struct {
	conn   net.Conn
	r      *bufio.Reader
	Header Header
	wmu    sync.Mutex
}

// Dial connects to rtl_tcp at addr and reads the header.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	return newClient(conn)
}

func newClient(conn net.Conn) (*Client, error) {
	c := &Client{conn: conn, r: bufio.NewReaderSize(conn, 1<<18)}
	var hdr [12]byte
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("rtl_tcp header: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if string(hdr[:4]) != "RTL0" {
		conn.Close()
		return nil, fmt.Errorf("rtl_tcp header: bad magic %q", hdr[:4])
	}
	c.Header = Header{
		TunerType: binary.BigEndian.Uint32(hdr[4:8]),
		GainCount: binary.BigEndian.Uint32(hdr[8:12]),
	}
	return c, nil
}

// Read reads raw cu8 IQ bytes.
func (c *Client) Read(p []byte) (int, error) { return c.r.Read(p) }

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Command sends one 5-byte command.
func (c *Client) Command(cmd byte, param uint32) error {
	var b [5]byte
	b[0] = cmd
	binary.BigEndian.PutUint32(b[1:], param)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.conn.Write(b[:])
	return err
}

// SetFreq tunes the center frequency in Hz.
func (c *Client) SetFreq(hz uint32) error { return c.Command(CmdFreq, hz) }

// SetSampleRate sets the sample rate in samples per second.
func (c *Client) SetSampleRate(sps uint32) error { return c.Command(CmdSampleRate, sps) }

// SetGain sets a manual tuner gain in dB, or automatic gain when db is nil.
func (c *Client) SetGain(db *float64) error {
	if db == nil {
		return c.Command(CmdGainMode, 0)
	}
	if err := c.Command(CmdGainMode, 1); err != nil {
		return err
	}
	return c.Command(CmdGain, uint32(int32(*db*10+0.5)))
}

// SetDirectSampling sets direct sampling mode (0 off, 1 I, 2 Q).
func (c *Client) SetDirectSampling(mode uint32) error { return c.Command(CmdDirectSampling, mode) }
