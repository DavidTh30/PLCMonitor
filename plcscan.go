package main

// Scan PLC: finds the data blocks (and the M, I and Q areas) a PLC lets us
// read over PUT/GET, measures their sizes and reads their bytes, so they can
// be looked at and picked as tags.
//
// S7 has no reliable way to list blocks on every CPU (the block list
// telegram works on S7-300/400 only), so data blocks are found by reading
// one byte of each candidate DB. Sizes are found by a binary search on the
// highest readable byte. Everything here only reads, and it runs through the
// monitor's connection between two polls (Monitor.Do), a batch at a time, so
// monitoring goes on while a scan runs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/robinson/gos7"
)

// maxAreaSize is the largest size looked for (a DB holds at most 64 KB).
const maxAreaSize = 65536

// PLCArea is one readable memory area found by a scan.
type PLCArea struct {
	Area string `json:"area"` // DB, M, I or Q
	DB   int    `json:"db,omitempty"`
	Size int    `json:"size"` // bytes
}

// PLCScanOptions are the settings of a scan.
type PLCScanOptions struct {
	From      int  `json:"from"`       // first DB number tried
	To        int  `json:"to"`         // last DB number tried
	Areas     bool `json:"areas"`      // also measure M, I and Q
	BlockList bool `json:"block_list"` // also ask the CPU for its DB list (S7-300/400)
}

// PLCScan is the state of the current or last scan.
type PLCScan struct {
	Running bool      `json:"running"`
	Phase   string    `json:"phase"` // what it is doing now
	Done    int       `json:"done"`  // DB numbers tried so far
	Total   int       `json:"total"` // DB numbers to try
	Found   []PLCArea `json:"found"`
	Listed  []int     `json:"listed,omitempty"` // DBs the CPU's block list named
	Note    string    `json:"note,omitempty"`
	Error   string    `json:"error,omitempty"`
	Target  string    `json:"target"`
	cancel  context.CancelFunc
}

// PLCScanner runs data block scans on the monitor's connection.
type PLCScanner struct {
	mon *Monitor
	mu  sync.Mutex
	st  PLCScan
}

func NewPLCScanner(m *Monitor) *PLCScanner {
	return &PLCScanner{mon: m, st: PLCScan{Found: []PLCArea{}}}
}

func (s *PLCScanner) State() PLCScan {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	st.Found = append([]PLCArea{}, s.st.Found...)
	return st
}

func (s *PLCScanner) Stop() {
	s.mu.Lock()
	if s.st.cancel != nil {
		s.st.cancel()
	}
	s.mu.Unlock()
}

// probeAddr is one byte to try reading.
type probeAddr struct {
	area Area
	db   int
	off  int
}

// byteProber is implemented by connections that can try many bytes in one
// request (S7: one multi-read telegram).
type byteProber interface {
	ProbeBytes(ps []probeAddr) ([]bool, error)
}

// Start begins a scan. It needs an S7 connection (or the simulator).
func (s *PLCScanner) Start(opt PLCScanOptions) error {
	c := s.mon.Config()
	if c.Protocol != ProtoS7 {
		return errors.New("Scan PLC works on ISO-TCP connections (data blocks); this is a Modbus connection")
	}
	if opt.From < 1 || opt.To > 65535 || opt.From > opt.To {
		return errors.New("DB numbers must be within 1..65535, from ≤ to")
	}
	if opt.To-opt.From >= 20000 {
		return errors.New("at most 20000 DB numbers per scan")
	}
	if st, _ := s.mon.Snapshot(); !st.Connected {
		return errors.New("not connected to the PLC")
	}
	s.mu.Lock()
	if s.st.Running {
		s.mu.Unlock()
		return errors.New("a scan is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	target := "simulator"
	if !c.Simulate {
		target = c.PLC.Address
	}
	s.st = PLCScan{Running: true, Phase: "finding data blocks", Total: opt.To - opt.From + 1,
		Found: []PLCArea{}, Target: target, cancel: cancel}
	s.mu.Unlock()
	log.Printf("SCAN PLC %s: DB%d..DB%d areas=%v blocklist=%v", target, opt.From, opt.To, opt.Areas, opt.BlockList)

	go func() {
		defer cancel()
		err := s.run(ctx, opt)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.st.Running, s.st.Phase = false, ""
		switch {
		case ctx.Err() != nil:
			s.st.Error = "stopped"
		case err != nil:
			s.st.Error = err.Error()
		}
		log.Printf("SCAN PLC: %d areas found %s", len(s.st.Found), s.st.Error)
	}()
	return nil
}

func (s *PLCScanner) set(f func(st *PLCScan)) {
	s.mu.Lock()
	f(&s.st)
	s.mu.Unlock()
}

func (s *PLCScanner) run(ctx context.Context, opt PLCScanOptions) error {
	// 1. The CPU's own block list, if asked for (and if it has one).
	var listed []int
	if opt.BlockList {
		s.set(func(st *PLCScan) { st.Phase = "asking the CPU for its block list" })
		err := s.mon.Do(func(conn PLC) error {
			var err error
			listed, err = listDBs(conn)
			return err
		})
		if err != nil {
			s.set(func(st *PLCScan) {
				st.Note = "The CPU gave no block list (" + err.Error() + "); DB numbers were tried one by one."
			})
		} else {
			s.set(func(st *PLCScan) { st.Listed = listed })
		}
	}

	// 2. Which DBs answer: read byte 0 of each candidate.
	var cand []int
	for n := opt.From; n <= opt.To; n++ {
		cand = append(cand, n)
	}
	for _, n := range listed {
		if n < opt.From || n > opt.To {
			cand = append(cand, n)
		}
	}
	var dbs []int
	const batch = 40
	for i := 0; i < len(cand); i += batch {
		if ctx.Err() != nil {
			return nil
		}
		part := cand[i:min(i+batch, len(cand))]
		ps := make([]probeAddr, len(part))
		for j, n := range part {
			ps[j] = probeAddr{AreaDB, n, 0}
		}
		ok, err := s.probe(ps)
		if err != nil {
			return err
		}
		for j, n := range part {
			if ok[j] {
				dbs = append(dbs, n)
			}
		}
		done := min(i+batch, len(cand))
		s.set(func(st *PLCScan) { st.Done = min(done, st.Total) })
	}

	// 3. Sizes, all areas at once.
	targets := make([]PLCArea, 0, len(dbs)+3)
	for _, n := range dbs {
		targets = append(targets, PLCArea{Area: "DB", DB: n})
	}
	if opt.Areas {
		for _, a := range []Area{AreaM, AreaI, AreaQ} {
			ok, err := s.probe([]probeAddr{{a, 0, 0}})
			if err != nil {
				return err
			}
			if ok[0] {
				targets = append(targets, PLCArea{Area: a.String()})
			}
		}
	}
	s.set(func(st *PLCScan) {
		st.Phase = fmt.Sprintf("measuring %d areas", len(targets))
		st.Done = st.Total
	})
	if err := s.measure(ctx, targets); err != nil {
		return err
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if (targets[i].Area == "DB") != (targets[j].Area == "DB") {
			return targets[i].Area == "DB"
		}
		return targets[i].DB < targets[j].DB
	})
	s.set(func(st *PLCScan) { st.Found = targets })
	return nil
}

// measure finds the size of every target: a binary search on the highest
// readable byte, one probe per target per round.
func (s *PLCScanner) measure(ctx context.Context, targets []PLCArea) error {
	lo := make([]int, len(targets)) // readable
	hi := make([]int, len(targets)) // not readable
	for i := range hi {
		hi[i] = maxAreaSize
	}
	for {
		var ps []probeAddr
		var idx []int
		for i, t := range targets {
			if hi[i]-lo[i] > 1 {
				a, _ := parseArea(t.Area)
				ps = append(ps, probeAddr{a, t.DB, (lo[i] + hi[i]) / 2})
				idx = append(idx, i)
			}
		}
		if len(ps) == 0 {
			break
		}
		if ctx.Err() != nil {
			return nil
		}
		ok, err := s.probe(ps)
		if err != nil {
			return err
		}
		for j, i := range idx {
			if ok[j] {
				lo[i] = ps[j].off
			} else {
				hi[i] = ps[j].off
			}
		}
	}
	for i := range targets {
		targets[i].Size = lo[i] + 1
	}
	return nil
}

// probe tries the bytes on the monitor's connection, in batches that each
// run between two polls.
func (s *PLCScanner) probe(ps []probeAddr) ([]bool, error) {
	out := make([]bool, 0, len(ps))
	const batch = 40
	for i := 0; i < len(ps); i += batch {
		part := ps[i:min(i+batch, len(ps))]
		var ok []bool
		err := s.mon.Do(func(conn PLC) error {
			var err error
			ok, err = probeBytes(conn, part)
			return err
		})
		if err != nil {
			return nil, err
		}
		out = append(out, ok...)
	}
	return out, nil
}

// probeBytes reports which bytes can be read. A refused read is "no"; an
// error is returned only when the connection itself fails.
func probeBytes(conn PLC, ps []probeAddr) ([]bool, error) {
	if p, ok := conn.(byteProber); ok {
		return p.ProbeBytes(ps)
	}
	out := make([]bool, len(ps))
	buf := make([]byte, 1)
	for i, p := range ps {
		err := conn.ReadBlock(&Block{Area: p.area, DB: p.db, Start: p.off, Size: 1}, buf)
		if err != nil && isLinkError(err) {
			return nil, err
		}
		out[i] = err == nil
	}
	return out, nil
}

// isLinkError tells a broken connection apart from a refused read.
func isLinkError(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, errSimDown) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "TCP :") || strings.Contains(s, "connection") || strings.Contains(s, "not connected")
}

// ProbeBytes tries up to 20 bytes per multi-read telegram (as many as fit in
// the negotiated PDU). Each item gets its own answer from the CPU.
func (p *s7PLC) ProbeBytes(ps []probeAddr) ([]bool, error) {
	per := (p.handler.PDULength - 19) / 12
	per = max(1, min(per, 20))
	out := make([]bool, len(ps))
	for i := 0; i < len(ps); i += per {
		part := ps[i:min(i+per, len(ps))]
		items := make([]gos7.S7DataItem, len(part))
		for j, q := range part {
			items[j] = gos7.S7DataItem{Area: s7AreaCode[q.area], WordLen: 0x02, DBNumber: q.db,
				Start: q.off, Amount: 1, Data: make([]byte, 2)}
		}
		if err := p.client.AGReadMulti(items, len(items)); err != nil {
			return nil, fmt.Errorf("probe: %w", err)
		}
		for j := range items {
			out[i+j] = items[j].Error == ""
		}
	}
	return out, nil
}

// listDBs asks the CPU for its list of data blocks. Only S7-300/400 (and
// compatible) CPUs answer; the reply is checked before it is trusted.
func listDBs(conn PLC) (dbs []int, err error) {
	p, ok := conn.(*s7PLC)
	if !ok {
		return nil, errors.New("no block list on this connection")
	}
	defer func() {
		if r := recover(); r != nil { // gos7 does not check short replies
			dbs, err = nil, fmt.Errorf("unexpected reply")
		}
	}()
	list, err := p.client.PGListBlocks()
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, n := range list.DBList {
		if n < 1 || n > 65535 || seen[n] {
			return nil, errors.New("unexpected reply")
		}
		seen[n] = true
		dbs = append(dbs, n)
	}
	if len(dbs) == 0 {
		return nil, errors.New("empty list")
	}
	sort.Ints(dbs)
	return dbs, nil
}

// ReadArea reads size bytes from an area (for the scan window's byte view).
func (s *PLCScanner) ReadArea(area string, db, start, size int) ([]byte, error) {
	a, err := parseArea(area)
	if err != nil || a.Protocol() != ProtoS7 {
		return nil, fmt.Errorf("unknown area %q", area)
	}
	if a != AreaDB {
		db = 0
	} else if db < 1 || db > 65535 {
		return nil, errors.New("DB number must be within 1..65535")
	}
	if start < 0 || size < 1 || start+size > maxAreaSize {
		return nil, errors.New("range must be within 0..65535")
	}
	if s.mon.Config().Protocol != ProtoS7 {
		return nil, errors.New("not an ISO-TCP connection")
	}
	buf := make([]byte, size)
	const chunk = 4096 // one Do per chunk, so polling goes on in between
	for off := 0; off < size; off += chunk {
		n := min(chunk, size-off)
		err := s.mon.Do(func(conn PLC) error {
			return conn.ReadBlock(&Block{Area: a, DB: db, Start: start + off, Size: n}, buf[off:off+n])
		})
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}
