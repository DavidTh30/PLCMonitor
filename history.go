package main

import "math"

// historySize is how many samples are kept per tag for the charts: one hour
// at the default 1 s poll interval, less at faster intervals.
const historySize = 3600

// Sample is one polled value. V is NaN when there was no good value (link
// down or read error), which the chart draws as a gap.
type Sample struct {
	T int64 // Unix milliseconds
	V float64
}

// history is a fixed-size ring of samples. Access is guarded by Monitor.mu.
type history struct {
	buf  []Sample
	head int // next write position
	n    int
}

func newHistory() *history { return &history{buf: make([]Sample, historySize)} }

func (h *history) add(s Sample) {
	// Collapse repeated gaps so a long outage costs one sample, not thousands.
	if math.IsNaN(s.V) && h.n > 0 && math.IsNaN(h.buf[(h.head-1+len(h.buf))%len(h.buf)].V) {
		return
	}
	h.buf[h.head] = s
	h.head = (h.head + 1) % len(h.buf)
	if h.n < len(h.buf) {
		h.n++
	}
}

// since returns the samples newer than t (Unix ms), oldest first.
func (h *history) since(t int64) []Sample {
	out := make([]Sample, 0, 64)
	start := (h.head - h.n + len(h.buf)) % len(h.buf)
	for i := 0; i < h.n; i++ {
		s := h.buf[(start+i)%len(h.buf)]
		if s.T > t {
			out = append(out, s)
		}
	}
	return out
}
