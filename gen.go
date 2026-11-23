package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"time"
)

// GenSpec describes a signal generator that keeps writing to one tag.
//
// BOOL tags:
//   - toggle ("loop delay"): TRUE for OnMs, FALSE for OffMs, repeating.
//   - tick: a TRUE pulse of WidthMs every PeriodMs, FALSE in between.
//
// Numeric tags (BYTE/SINT, WORD/INT, DWORD/DINT, REAL/LREAL), between Min
// and Max with period PeriodMs, written every IntervalMs:
//   - sine, square (Max for the first half period, Min for the second),
//     sawtooth (ramps Min→Max, then jumps back), random (new value each write).
type GenSpec struct {
	Kind       string  `json:"kind"`
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
	PeriodMs   int     `json:"period_ms"`
	IntervalMs int     `json:"interval_ms"`
	OnMs       int     `json:"on_ms"`
	OffMs      int     `json:"off_ms"`
	WidthMs    int     `json:"width_ms"`
}

// minWriteMs is the fastest a generator may write, to keep the PLC/bus usable.
const minWriteMs = 50

type generator struct {
	spec    GenSpec
	addr    string // tag address and type when started; a change stops the generator
	typ     string
	summary string
	lastErr string
	writes  int
	cancel  context.CancelFunc
}

func (g GenSpec) validate(t *Tag) error {
	isBool := t.dtype == TypeBool
	switch g.Kind {
	case "toggle", "tick":
		if !isBool {
			return fmt.Errorf("%s is for BOOL tags; %s is %s", g.Kind, t.Name, t.Type)
		}
	case "sine", "square", "sawtooth", "random":
		if isBool {
			return fmt.Errorf("%s is for numeric tags; use toggle or tick for BOOL", g.Kind)
		}
	default:
		return fmt.Errorf("unknown generator %q (BOOL: toggle, tick; numbers: sine, square, sawtooth, random)", g.Kind)
	}
	switch g.Kind {
	case "toggle":
		if g.OnMs < minWriteMs || g.OffMs < minWriteMs {
			return fmt.Errorf("on and off times must be at least %d ms", minWriteMs)
		}
	case "tick":
		if g.WidthMs < minWriteMs || g.PeriodMs < 2*minWriteMs || g.WidthMs >= g.PeriodMs {
			return fmt.Errorf("pulse width must be at least %d ms and shorter than the period", minWriteMs)
		}
	default:
		if g.IntervalMs < minWriteMs {
			return fmt.Errorf("write interval must be at least %d ms", minWriteMs)
		}
		if g.Kind != "random" && g.PeriodMs < 2*g.IntervalMs {
			return fmt.Errorf("period must be at least twice the write interval")
		}
		if !(g.Min < g.Max) {
			return fmt.Errorf("min must be smaller than max")
		}
		if lo, hi := t.dtype.Range(); g.Min < lo || g.Max > hi {
			return fmt.Errorf("min/max must be within the %s range (%g..%g)", t.Type, lo, hi)
		}
	}
	return nil
}

func (g GenSpec) String() string {
	ms := func(n int) string { return (time.Duration(n) * time.Millisecond).String() }
	switch g.Kind {
	case "toggle":
		return fmt.Sprintf("toggle %s on / %s off", ms(g.OnMs), ms(g.OffMs))
	case "tick":
		return fmt.Sprintf("tick %s pulse every %s", ms(g.WidthMs), ms(g.PeriodMs))
	case "random":
		return fmt.Sprintf("random %g..%g every %s", g.Min, g.Max, ms(g.IntervalMs))
	}
	return fmt.Sprintf("%s %g..%g, period %s", g.Kind, g.Min, g.Max, ms(g.PeriodMs))
}

// value returns the generator output at elapsed time e.
func (g GenSpec) value(t *Tag, e time.Duration) any {
	ms := float64(e.Milliseconds())
	switch g.Kind {
	case "toggle":
		return math.Mod(ms, float64(g.OnMs+g.OffMs)) < float64(g.OnMs)
	case "tick":
		return math.Mod(ms, float64(g.PeriodMs)) < float64(g.WidthMs)
	}
	var v float64
	phase := math.Mod(ms, float64(g.PeriodMs)) / float64(g.PeriodMs) // 0..1
	switch g.Kind {
	case "sine":
		v = (g.Min+g.Max)/2 + (g.Max-g.Min)/2*math.Sin(2*math.Pi*phase)
	case "square":
		v = g.Max
		if phase >= 0.5 {
			v = g.Min
		}
	case "sawtooth":
		v = g.Min + (g.Max-g.Min)*phase
	case "random":
		v = g.Min + rand.Float64()*(g.Max-g.Min)
	}
	if t.dtype.IsFloat() {
		return v
	}
	return int64(math.Round(math.Max(g.Min, math.Min(g.Max, v))))
}

// StartGenerator starts (or replaces) the signal generator on a tag.
func (m *Monitor) StartGenerator(name string, spec GenSpec) error {
	t, ok := m.Tag(name)
	if !ok {
		return notFound(fmt.Sprintf("tag %q not found", name))
	}
	if !t.Writable() {
		return fmt.Errorf("%s (%s) is read-only", t.Name, t.Address())
	}
	if err := spec.validate(&t); err != nil {
		return err
	}
	m.mu.RLock()
	ctx := m.runCtx
	m.mu.RUnlock()
	if ctx == nil {
		ctx = context.Background()
	}
	gctx, cancel := context.WithCancel(ctx)
	g := &generator{spec: spec, addr: t.Address(), typ: t.Type, summary: spec.String(), cancel: cancel}
	m.mu.Lock()
	if old := m.gens[name]; old != nil {
		old.cancel()
	}
	m.gens[name] = g
	m.mu.Unlock()
	log.Printf("GENERATOR %s %s: %s", t.Name, t.Address(), g.summary)
	go m.runGenerator(gctx, name, g)
	return nil
}

// StopGenerator stops the generator on a tag ("" stops all). The tag keeps
// the last value written.
func (m *Monitor) StopGenerator(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, g := range m.gens {
		if name == "" || k == name {
			g.cancel()
			delete(m.gens, k)
			n++
			log.Printf("GENERATOR %s stopped", k)
		}
	}
	return n
}

func (m *Monitor) runGenerator(ctx context.Context, name string, g *generator) {
	step := time.Duration(g.spec.IntervalMs) * time.Millisecond
	if g.spec.Kind == "toggle" || g.spec.Kind == "tick" {
		step = 10 * time.Millisecond // check often, write only on edges
	}
	tick := time.NewTicker(step)
	defer tick.Stop()
	start := time.Now()
	var last any
	for {
		t, ok := m.Tag(name)
		if !ok || t.Address() != g.addr || t.Type != g.typ {
			log.Printf("GENERATOR %s stopped: tag removed or changed", name)
			m.removeGenerator(name, g)
			return
		}
		v := g.spec.value(&t, time.Since(start))
		if b, isBool := v.(bool); !isBool || last == nil || b != last {
			err := m.Write(name, v)
			m.mu.Lock()
			if err != nil {
				g.lastErr = err.Error()
			} else {
				g.lastErr, g.writes, last = "", g.writes+1, v
			}
			m.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (m *Monitor) removeGenerator(name string, g *generator) {
	m.mu.Lock()
	if m.gens[name] == g {
		delete(m.gens, name)
	}
	m.mu.Unlock()
	g.cancel()
}
