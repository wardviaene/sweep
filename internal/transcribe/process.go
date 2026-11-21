package transcribe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// process runs whisper-server as a child on 127.0.0.1 and port of its own.
type process struct {
	bin, model string
	threads    int

	startMu sync.Mutex // serialises ensure (the model load takes seconds)
	mu      sync.Mutex
	cmd     *exec.Cmd
	url     string
	done    chan struct{}
	state   string // stopped | running | error
	lastEr  string
}

type tailBuf struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = t.b[len(t.b)-4096:]
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *tailBuf) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.b)), "\n")
	return strings.Join(lines[max(0, len(lines)-3):], " | ")
}

func (p *process) status() (state, lastErr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == "" {
		return "stopped", ""
	}
	return p.state, p.lastEr
}

// ensure starts the server when it is not running and returns its URL.
func (p *process) ensure(ctx context.Context) (string, error) {
	p.startMu.Lock()
	defer p.startMu.Unlock()
	p.mu.Lock()
	if p.cmd != nil {
		select {
		case <-p.done:
			p.cmd = nil
		default:
			u := p.url
			p.mu.Unlock()
			return u, nil
		}
	}
	p.mu.Unlock()
	if _, err := os.Stat(p.model); err != nil {
		return "", p.fail(fmt.Errorf("model %s not found (run make whisper-model)", p.model))
	}
	bin, err := exec.LookPath(p.bin)
	if err != nil {
		return "", p.fail(fmt.Errorf("%s not found (brew install whisper-cpp)", p.bin))
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", p.fail(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	threads := p.threads
	if threads <= 0 {
		threads = 4
	}
	// -sns drops non-speech tokens such as [MUSIC]; the prompt is sent per request.
	cmd := exec.Command(bin, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "-m", p.model,
		"-t", strconv.Itoa(threads), "-sns", "-l", "en")
	tail := &tailBuf{}
	cmd.Stdout, cmd.Stderr = tail, tail
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return "", p.fail(err)
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	url := "http://127.0.0.1:" + strconv.Itoa(port)
	// The port opens once the model is loaded.
	deadline := time.Now().Add(90 * time.Second)
	for {
		select {
		case <-done:
			return "", p.fail(fmt.Errorf("whisper-server exited: %s", tail.last()))
		case <-ctx.Done():
			killGroup(cmd)
			<-done
			return "", ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if resp, err := http.Get(url + "/"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			killGroup(cmd)
			<-done
			return "", p.fail(errors.New("whisper-server did not start within 90 s"))
		}
	}
	p.mu.Lock()
	p.cmd, p.url, p.done, p.state, p.lastEr = cmd, url, done, "running", ""
	p.mu.Unlock()
	go func() { // note an unexpected exit
		<-done
		p.mu.Lock()
		if p.cmd == cmd {
			p.cmd = nil
			if p.state == "running" {
				p.state, p.lastEr = "error", "whisper-server exited: "+tail.last()
			}
		}
		p.mu.Unlock()
	}()
	return url, nil
}

func (p *process) fail(err error) error {
	p.mu.Lock()
	p.state, p.lastEr = "error", err.Error()
	p.mu.Unlock()
	return err
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

// stop terminates the child.
func (p *process) stop() {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	if cmd != nil {
		p.cmd, p.state = nil, "stopped"
	}
	p.mu.Unlock()
	if cmd == nil {
		return
	}
	killGroup(cmd)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}
