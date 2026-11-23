package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Simulator is an in-memory stand-in for a PLC. It owns byte images of the
// DB, I, Q and M areas and writes changing values into the configured tag
// positions, so reads go through the same decode path as a real PLC.
type Simulator struct {
	mu        sync.Mutex
	mem       map[[3]int][]byte // {area, db, modbus unit} -> bytes
	tags      []Tag
	cfg       SimConfig
	started   time.Time
	downUntil time.Time
	written   map[[5]int]bool // tags written by the user: no longer animated
}

var errSimDown = errors.New("simulated PLC unreachable (link drop)")

func NewSimulator(tags []Tag, cfg SimConfig) *Simulator {
	s := &Simulator{mem: map[[3]int][]byte{}, written: map[[5]int]bool{}, cfg: cfg, started: time.Now()}
	s.SetTags(tags)
	return s
}

// SetTags switches the simulated values to a new tag list, growing the
// simulated memory areas as needed. Existing memory contents are kept.
func (s *Simulator) SetTags(tags []Tag) {
	s.mu.Lock()
	s.tags = append([]Tag(nil), tags...)
	for i := range s.tags {
		t := &s.tags[i]
		k := [3]int{int(t.area), t.DB, t.Unit}
		_, need := t.imageRange()
		if len(s.mem[k]) < need {
			grown := make([]byte, need+64)
			copy(grown, s.mem[k])
			s.mem[k] = grown
		}
	}
	s.mu.Unlock()
	s.step(time.Now())
}

// Run updates simulated values until ctx is cancelled.
func (s *Simulator) Run(ctx context.Context) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	nextDrop := time.Now().Add(s.cfg.DropEvery)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			s.mu.Lock()
			if s.cfg.DropEvery > 0 && now.After(nextDrop) {
				s.downUntil = now.Add(s.cfg.DownFor)
				nextDrop = now.Add(s.cfg.DropEvery)
			}
			s.mu.Unlock()
			s.step(now)
		}
	}
}

func (s *Simulator) step(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	el := now.Sub(s.started).Seconds()
	for i := range s.tags {
		t := &s.tags[i]
		if s.written[simKey(t)] {
			continue // keeps the value the user wrote
		}
		buf := s.mem[[3]int{int(t.area), t.DB, t.Unit}]
		phase := float64(i)
		var v any
		switch {
		case t.dtype == TypeBool:
			period := float64(2 + i%5) // toggle every 2..6 s
			v = int(el/period)%2 == 1
		case t.dtype.IsFloat():
			v = 50 + 25*math.Sin(2*math.Pi*el/20+phase)
		default:
			// Integers count up once per second, wrapping inside the type's range.
			n := int64(el) + int64(i)*10
			switch t.dtype {
			case TypeByte:
				n %= 256
			case TypeSInt:
				n = n%256 - 128
			case TypeInt:
				n = n%2000 - 1000
			}
			v = n
		}
		t.Encode(buf, v)
	}
}

// Dialer returns a Dialer that "connects" to this simulator.
func (s *Simulator) Dialer() Dialer {
	return func() (PLC, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if time.Now().Before(s.downUntil) {
			return nil, errSimDown
		}
		return &simConn{s: s}, nil
	}
}

type simConn struct{ s *Simulator }

func (c *simConn) ReadBlock(b *Block, buf []byte) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if time.Now().Before(c.s.downUntil) {
		return errSimDown
	}
	mem := c.s.mem[[3]int{int(b.Area), b.DB, b.Unit}]
	if b.Start+b.Size > len(mem) {
		return errors.New("simulated PLC: address out of range")
	}
	copy(buf, mem[b.Start:b.Start+b.Size])
	return nil
}

// WriteTag stores the value in simulated memory. From then on the simulator
// stops animating that tag, so the written value (or a signal generator's
// output) is what gets read back.
func (c *simConn) WriteTag(t *Tag, v any) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if time.Now().Before(c.s.downUntil) {
		return errSimDown
	}
	if t.area == AreaIR || t.area == AreaDI {
		return fmt.Errorf("write %s: %s is read-only in Modbus", t.Address(), t.area)
	}
	mem := c.s.mem[[3]int{int(t.area), t.DB, t.Unit}]
	if _, end := t.imageRange(); end > len(mem) {
		return errors.New("simulated PLC: address out of range")
	}
	t.Encode(mem, v)
	c.s.written[simKey(t)] = true
	return nil
}

func simKey(t *Tag) [5]int { return [5]int{int(t.area), t.DB, t.Unit, t.Offset, t.Bit} }

func (c *simConn) Close() error { return nil }
