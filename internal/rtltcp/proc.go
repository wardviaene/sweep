package rtltcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Process supervises one rtl_tcp subprocess.
type Process struct {
	cmd    *exec.Cmd
	Addr   string
	mu     sync.Mutex
	device string
	done   chan struct{}
	err    error
}

var deviceLine = regexp.MustCompile(`^\s+0:\s+(.+)$`)

// Start launches rtl_tcp on 127.0.0.1:port and connects to it as its only client.
// rtl_tcp buffers its stdout when piped, so readiness is detected by retrying the
// connection rather than waiting for "listening...". Stop kills the process.
func Start(ctx context.Context, bin string, port int, freqHz, sampleRate uint32, extra ...string) (*Process, *Client, error) {
	args := []string{"-a", "127.0.0.1", "-p", strconv.Itoa(port),
		"-f", strconv.FormatUint(uint64(freqHz), 10), "-s", strconv.FormatUint(uint64(sampleRate), 10)}
	args = append(args, extra...)
	cmd := exec.CommandContext(ctx, bin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdout = nil
	p := &Process{cmd: cmd, Addr: fmt.Sprintf("127.0.0.1:%d", port), done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", bin, err)
	}
	go p.scan(stderr)
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()

	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-p.done:
			return nil, nil, fmt.Errorf("rtl_tcp exited: %v (is the dongle plugged in?)", p.err)
		case <-deadline:
			p.Stop()
			return nil, nil, fmt.Errorf("rtl_tcp did not accept a connection within 10s")
		case <-ctx.Done():
			p.Stop()
			return nil, nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		c, err := Dial(p.Addr, time.Second)
		if err == nil {
			return p, c, nil
		}
	}
}

func (p *Process) scan(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		log.Printf("rtl_tcp: %s", line)
		if m := deviceLine.FindStringSubmatch(line); m != nil {
			p.mu.Lock()
			p.device = m[1]
			p.mu.Unlock()
		}
	}
}

// Device returns the device description rtl_tcp printed, e.g. "Nooelec, NESDR SMArt v5, SN: 123".
func (p *Process) Device() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.device
}

// Done is closed when the process exits.
func (p *Process) Done() <-chan struct{} { return p.done }

// Stop kills the process and waits for it.
func (p *Process) Stop() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	<-p.done
}
