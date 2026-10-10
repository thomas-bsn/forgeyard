package agent

import (
	"bytes"
	"sync"
)

// Logs keeps the agent's last log lines, which the control plane can show without a shell on the node.
// The agent's logger writes to it as well as to its output.
var Logs = &logRing{max: 1000, subs: map[chan string]struct{}{}}

type logRing struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial []byte
	subs    map[chan string]struct{}
}

// Write takes the logger's output, one line or more at a time.
func (r *logRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.partial = append(r.partial, p...)
	for {
		i := bytes.IndexByte(r.partial, '\n')
		if i < 0 {
			break
		}
		line := string(r.partial[:i])
		r.partial = r.partial[i+1:]
		r.lines = append(r.lines, line)
		if len(r.lines) > r.max {
			r.lines = r.lines[len(r.lines)-r.max:]
		}
		for ch := range r.subs {
			select {
			case ch <- line:
			default: // a slow reader misses lines rather than slowing the agent
			}
		}
	}
	return len(p), nil
}

// follow returns the last tail lines and a channel of the next ones, until stop is called.
func (r *logRing) follow(tail int) ([]string, <-chan string, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := max(0, len(r.lines)-tail)
	past := append([]string(nil), r.lines[start:]...)
	ch := make(chan string, 256)
	r.subs[ch] = struct{}{}
	return past, ch, func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}
}
