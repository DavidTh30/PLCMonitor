package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

// TagState is the latest known value of one tag.
type TagState struct {
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Type        string    `json:"type"`
	Description string    `json:"description,omitempty"`
	Value       any       `json:"value"`
	Display     string    `json:"display"`
	Good        bool      `json:"good"`
	Error       string    `json:"error,omitempty"`
	Updated     time.Time `json:"updated"`
	Changed     time.Time `json:"changed"`
	Changes     int       `json:"changes"`
	Writable    bool      `json:"writable"`
	Gen         string    `json:"gen,omitempty"`       // running signal generator, e.g. "sine 0..100 / 10s"
	GenError    string    `json:"gen_error,omitempty"` // its last write error
	raw         any       // last decoded value, before jsonSafe
	hist        *history  // samples for the chart
}

// Status describes the PLC link.
type Status struct {
	Target       string    `json:"target"`
	Simulated    bool      `json:"simulated"`
	Connected    bool      `json:"connected"`
	LastError    string    `json:"last_error,omitempty"`
	Reconnects   int       `json:"reconnects"`
	ConnectedAt  time.Time `json:"connected_at"`
	LastPoll     time.Time `json:"last_poll"`
	PollDuration string    `json:"poll_duration"`
	PollInterval string    `json:"poll_interval"`
	Blocks       int       `json:"blocks"`
	ReadOnly     bool      `json:"read_only"`
	Protocol     string    `json:"protocol"`
	NetworkFile  string    `json:"network_file"`
	TagsFile     string    `json:"tags_file"`
	NetworkDirty bool      `json:"network_dirty"` // differs from NetworkFile
	TagsDirty    bool      `json:"tags_dirty"`    // differs from TagsFile
	ReadMs       float64   `json:"read_ms"`       // last poll's read time, for the Status window
	Polls        int64     `json:"polls"`         // completed polls since start
	Paused       bool      `json:"paused"`        // disconnected with the Disconnect button
	pollDur      time.Duration
}

// Change is emitted whenever a tag value or its quality changes.
type Change struct {
	Time    time.Time
	Tag     *Tag
	Old     any
	New     any
	Good    bool
	Address string
}

// errReload stops the poll loop so it can pick up a new config.
var errReload = errors.New("config changed")

type Monitor struct {
	dialFor func(*Config) Dialer
	sinks   *Dispatcher
	path    string // config file to save edits to; "" keeps them in memory only
	reload  chan struct{}

	// OnApply, if set, runs with each new config before it takes effect
	// (the simulator uses it to lay out memory for new tags).
	OnApply func(*Config)

	editMu sync.Mutex // serialises Update calls

	mu     sync.RWMutex
	cfg    *Config
	gen    int // bumped on every config change; stale polls are discarded
	blocks []*Block
	states []TagState // same order as cfg.Tags
	index  map[*Tag]int
	status Status

	// Serialized network/tag settings as last read from or written to
	// NetworkFile/TagsFile, to tell whether there are unsaved changes.
	netBase, tagBase string

	writes chan writeReq         // served by the poll loop between reads
	jobs   chan connJob          // other work on the connection (e.g. bus scans), same rule
	gens   map[string]*generator // running signal generators by tag name (guarded by mu)
	runCtx context.Context       // lifetime of Run; generators stop with it

	// Trend, if set, records every polled value to the history files.
	Trend *TrendStore

	layoutRev int // see Layout (guarded by mu)
}

type connJob struct {
	fn   func(PLC) error
	done chan error
}

// Do runs fn with the live connection, between two polls. It fails if
// there is no connection.
func (m *Monitor) Do(fn func(PLC) error) error {
	m.mu.RLock()
	connected := m.status.Connected
	m.mu.RUnlock()
	if !connected {
		return errors.New("not connected")
	}
	job := connJob{fn: fn, done: make(chan error, 1)}
	select {
	case m.jobs <- job:
	case <-time.After(10 * time.Second):
		return errors.New("the PLC/device is busy or not connected")
	}
	return <-job.done
}

type writeReq struct {
	tag   string
	value any
	done  chan error
}

func NewMonitor(cfg *Config, dialFor func(*Config) Dialer, sinks *Dispatcher, path string) *Monitor {
	m := &Monitor{dialFor: dialFor, sinks: sinks, path: path, reload: make(chan struct{}, 1),
		writes: make(chan writeReq), jobs: make(chan connJob), gens: map[string]*generator{}}
	// Without auto connect the program starts disconnected, as after Disconnect.
	m.status = Status{ReadOnly: cfg.Web.ReadOnly, Paused: cfg.Web.NoAutoConnect}
	m.install(cfg)
	m.loadBaselines()
	return m
}

// install swaps in cfg, keeping the live state of tags whose address and type
// did not change. Callers hold m.mu (or own m exclusively).
func (m *Monitor) install(cfg *Config) {
	old := map[string]TagState{}
	if m.cfg != nil {
		for i, t := range m.cfg.Tags {
			old[t.Name+"|"+t.Address()+"|"+t.Type] = m.states[i]
		}
	}
	m.cfg = cfg
	m.gen++
	m.blocks = BuildBlocks(cfg.Tags, cfg.Modbus.MaxGap)
	m.states = make([]TagState, len(cfg.Tags))
	m.index = map[*Tag]int{}
	for i := range cfg.Tags {
		t := &cfg.Tags[i]
		m.index[t] = i
		s, ok := old[t.Name+"|"+t.Address()+"|"+t.Type]
		if !ok {
			s = TagState{Name: t.Name, Address: t.Address(), Type: t.Type, Display: "-", hist: newHistory()}
		}
		s.Description = t.Description
		s.Writable = t.Writable()
		if t.inactive != "" {
			s.Good, s.Error, s.Display, s.Value, s.raw = false, "not polled: "+t.inactive, "-", nil, nil
		}
		m.states[i] = s
	}
	m.status.Target = targetName(cfg)
	m.status.Simulated = cfg.Simulate
	m.status.Protocol = cfg.Protocol
	m.status.PollInterval = cfg.PollInterval.String()
	m.status.Blocks = len(m.blocks)
	m.status.NetworkFile, m.status.TagsFile = cfg.NetworkFile, cfg.TagsFile
}

func targetName(cfg *Config) string {
	if cfg.Simulate {
		return "simulator (" + protoName(cfg.Protocol) + ")"
	}
	if missingTarget(cfg) != "" {
		return protoName(cfg.Protocol) + " – no address set"
	}
	if cfg.Protocol == ProtoModbus {
		return modbusTarget(cfg.Modbus)
	}
	if cfg.Protocol == ProtoOPCUA {
		return "OPC UA " + cfg.OPCUA.Endpoint
	}
	return fmt.Sprintf("ISO-TCP %s rack %d slot %d", cfg.PLC.Address, cfg.PLC.Rack, cfg.PLC.Slot)
}

// History returns the chart samples of a tag newer than since (Unix ms).
func (m *Monitor) History(name string, since int64) (TagState, []Sample, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.states {
		if s.Name == name {
			return s, s.hist.since(since), true
		}
	}
	return TagState{}, nil, false
}

// Layout returns the window layout and its revision, which goes up on every
// change so other windows can tell they need to refresh.
func (m *Monitor) Layout() (LayoutConfig, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Layout, m.layoutRev
}

// SetLayout stores a new window layout and saves the config file. Unlike
// Update it does not touch polling: only the layout changes.
func (m *Monitor) SetLayout(l LayoutConfig) (int, error) {
	if err := l.validate(); err != nil {
		return 0, err
	}
	m.editMu.Lock()
	defer m.editMu.Unlock()
	m.mu.Lock()
	m.cfg.Layout = l // replaced as a whole, never edited in place, so clones may share it
	m.layoutRev++
	rev, snapshot := m.layoutRev, m.cfg.clone()
	m.mu.Unlock()
	if m.path != "" {
		if err := SaveConfig(m.path, snapshot); err != nil {
			return rev, fmt.Errorf("save config: %w", err)
		}
	}
	return rev, nil
}

// ExportStatus describes the running exporters (CSV, MQTT, InfluxDB).
func (m *Monitor) ExportStatus() []SinkStatus {
	if m.sinks == nil {
		return []SinkStatus{}
	}
	return m.sinks.Status()
}

// Config returns a copy of the running config.
func (m *Monitor) Config() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.clone()
}

// Update applies edit to a copy of the running config, validates it, saves it
// to the config file and makes it live without restarting. Changing tags keeps
// the PLC connection; changing network settings reconnects.
func (m *Monitor) Update(edit func(c *Config) error) error {
	m.editMu.Lock()
	defer m.editMu.Unlock()
	prev := m.Config()
	next := prev.clone()
	if err := edit(next); err != nil {
		return err
	}
	next.applyDefaults()
	if err := next.validate(); err != nil {
		return err
	}
	if m.sinks != nil {
		// New exporters first: if e.g. the CSV file can't be opened, the
		// change is refused and nothing is saved. (No-op when unchanged.)
		if err := m.sinks.Reconfigure(next.Export); err != nil {
			return err
		}
	}
	if m.path != "" {
		if err := SaveConfig(m.path, next); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
	}
	if m.OnApply != nil {
		m.OnApply(next)
	}
	m.mu.Lock()
	m.install(next)
	m.mu.Unlock()
	if !sameNetwork(prev, next) {
		// Never carry generators over to another PLC/device.
		if n := m.StopGenerator(""); n > 0 {
			log.Printf("network settings changed: stopped %d signal generator(s)", n)
		}
	}
	select {
	case m.reload <- struct{}{}:
	default:
	}
	return nil
}

// Snapshot returns a copy of the link status and all tag states.
func (m *Monitor) Snapshot() (Status, []TagState) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := m.status
	st.PollDuration = st.pollDur.Round(time.Microsecond).String()
	st.ReadMs = float64(st.pollDur.Microseconds()) / 1000
	st.NetworkDirty = string(marshalNetwork(m.cfg)) != m.netBase
	st.TagsDirty = string(marshalTags(m.cfg)) != m.tagBase
	states := append([]TagState{}, m.states...) // [] not null in JSON when there are no tags
	for i := range states {
		if g := m.gens[states[i].Name]; g != nil {
			states[i].Gen, states[i].GenError = g.summary, g.lastErr
		}
	}
	return st, states
}

// errNotConnected is returned for writes while there is no connection.
var errNotConnected = errors.New("not connected to the PLC/device; nothing was written")

// Write writes v (from Tag.ParseValue) to the named tag. The write is done
// by the poll loop between two polls, so it never overlaps a read.
func (m *Monitor) Write(name string, v any) error {
	m.mu.RLock()
	connected := m.status.Connected
	m.mu.RUnlock()
	if !connected {
		return errNotConnected
	}
	req := writeReq{tag: name, value: v, done: make(chan error, 1)}
	select {
	case m.writes <- req:
	case <-time.After(5 * time.Second):
		return errors.New("the PLC/device is busy or not connected; nothing was written")
	}
	return <-req.done
}

// Tag returns a copy of the named tag from the running config.
func (m *Monitor) Tag(name string) (Tag, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.cfg.Tags {
		if t.Name == name {
			return t, true
		}
	}
	return Tag{}, false
}

func (m *Monitor) execWrite(conn PLC, req writeReq) error {
	t, ok := m.Tag(req.tag)
	switch {
	case !ok:
		return notFound(fmt.Sprintf("tag %q not found", req.tag))
	case t.inactive != "":
		return fmt.Errorf("tag %s is not polled: %s", t.Name, t.inactive)
	case !t.Writable():
		return fmt.Errorf("%s (%s) is read-only", t.Name, t.Address())
	}
	return conn.WriteTag(&t, req.value)
}

// Run connects, polls and reconnects until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	m.mu.Lock()
	m.runCtx = ctx
	m.mu.Unlock()
	cfg := m.Config()
	delay := cfg.ReconnectDelay
	first := true
	for ctx.Err() == nil {
		// Disconnected by the user (Disconnect button): no traffic at all
		// until Connect.
		if m.isPaused() {
			if !m.waitResume(ctx) {
				return
			}
			first, delay = true, m.Config().ReconnectDelay
			continue
		}
		cfg = m.Config()
		if !first {
			m.mu.Lock()
			m.status.Reconnects++
			m.mu.Unlock()
		}
		first = false

		conn, err := m.dialFor(cfg)()
		if err != nil {
			m.setDisconnected(err)
			log.Printf("PLC connect failed: %v (retry in %s)", err, delay)
			if !m.wait(ctx, delay) {
				return
			}
			delay = min(delay*2, cfg.ReconnectMax)
			continue
		}
		delay = cfg.ReconnectDelay
		m.mu.Lock()
		m.status.Connected, m.status.LastError, m.status.ConnectedAt = true, "", time.Now()
		m.mu.Unlock()
		log.Printf("PLC connected (%s)", targetName(cfg))

		for {
			err = m.pollLoop(ctx, conn)
			if !errors.Is(err, errReload) {
				break
			}
			if m.isPaused() {
				err = errPaused
				break
			}
			now := m.Config()
			if sameNetwork(cfg, now) {
				cfg = now
				log.Printf("config updated: %d tags", len(cfg.Tags))
				continue
			}
			log.Printf("network settings changed, connecting to %s", targetName(now))
			first = true // a deliberate reconnect, not a failure
			break
		}
		conn.Close()
		if err == nil { // ctx cancelled
			return
		}
		if errors.Is(err, errPaused) {
			m.setDisconnected(err)
			m.mu.Lock()
			m.status.LastError = "" // not a fault
			m.mu.Unlock()
			log.Printf("PLC disconnected by the user (%s)", targetName(cfg))
			continue
		}
		if errors.Is(err, errReload) {
			m.mu.Lock()
			m.status.Connected = false
			m.mu.Unlock()
			continue
		}
		m.setDisconnected(err)
		log.Printf("PLC link lost: %v (reconnecting in %s)", err, delay)
		if !m.wait(ctx, delay) {
			return
		}
	}
}

// errPaused ends the connection when the user presses Disconnect.
var errPaused = errors.New("disconnected by the user")

func (m *Monitor) isPaused() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status.Paused
}

// SetPaused disconnects from the PLC/device and stops all traffic (true), or
// connects again at once (false). Running signal generators are stopped.
func (m *Monitor) SetPaused(p bool) {
	m.mu.Lock()
	was := m.status.Paused
	m.status.Paused = p
	m.mu.Unlock()
	if was == p {
		return
	}
	if p {
		if n := m.StopGenerator(""); n > 0 {
			log.Printf("disconnect: stopped %d signal generator(s)", n)
		}
	}
	// Wake the poll loop, a reconnect back-off or the paused wait.
	select {
	case m.reload <- struct{}{}:
	default:
	}
}

// waitResume blocks until Connect is pressed (or the program stops).
func (m *Monitor) waitResume(ctx context.Context) bool {
	for m.isPaused() {
		select {
		case <-ctx.Done():
			return false
		case <-m.reload:
		}
	}
	return true
}

func sameNetwork(a, b *Config) bool {
	return a.Protocol == b.Protocol && a.PLC == b.PLC && a.Modbus == b.Modbus && a.OPCUA == b.OPCUA && a.Simulate == b.Simulate
}

func (m *Monitor) pollLoop(ctx context.Context, conn PLC) error {
	m.mu.RLock()
	gen, blocks, interval := m.gen, m.blocks, m.cfg.PollInterval
	m.mu.RUnlock()
	if len(blocks) == 0 {
		log.Printf("no tags configured; add some on the dashboard")
	} else {
		log.Printf("polling %d block(s) every %s", len(blocks), interval)
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()
	bufs := make([][]byte, len(blocks))
	for i, b := range blocks {
		bufs[i] = make([]byte, b.Size)
	}
	blockErrs := make([]error, len(blocks))
	for {
		start := time.Now()
		silent := 0 // blocks whose device didn't answer at all
		for i, b := range blocks {
			err := conn.ReadBlock(b, bufs[i])
			var be *blockError
			if errors.As(err, &be) {
				if blockErrs[i] == nil || blockErrs[i].Error() != err.Error() {
					log.Printf("read error (link still up): %v", err)
				}
				blockErrs[i] = err
				if be.noReply {
					silent++
				}
				continue
			}
			if err != nil {
				return err
			}
			if blockErrs[i] != nil {
				log.Printf("read %s: OK again", blockName(b))
			}
			blockErrs[i] = nil
		}
		// On an RS485 bus a single dead slave is tolerated, but if nothing
		// answered the line itself (adapter, cable, gateway) is likely down.
		if silent > 0 && silent == len(blocks) {
			return fmt.Errorf("no device answered: %w", blockErrs[0])
		}
		m.update(gen, blocks, start, time.Since(start), bufs, blockErrs)
	wait:
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-m.reload:
				return errReload
			case req := <-m.writes:
				req.done <- m.execWrite(conn, req)
			case job := <-m.jobs:
				job.done <- job.fn(conn)
			case <-tick.C:
				break wait
			}
		}
	}
}

func (m *Monitor) update(gen int, blocks []*Block, now time.Time, dur time.Duration, bufs [][]byte, blockErrs []error) {
	var changes []Change
	m.mu.Lock()
	if gen != m.gen { // config changed while we were reading
		m.mu.Unlock()
		return
	}
	m.status.LastPoll, m.status.pollDur = now, dur
	m.status.Polls++
	ms := now.UnixMilli()
	for bi, b := range blocks {
		for ti, t := range b.Tags {
			s := &m.states[m.index[t]]
			v, err := t.Decode(bufs[bi], b.Start)
			if blockErrs[bi] != nil {
				err = blockErrs[bi]
			} else if ti < len(b.TagErrs) && b.TagErrs[ti] != nil {
				err = b.TagErrs[ti]
			}
			if err != nil {
				s.hist.add(Sample{ms, math.NaN()})
				m.record(t.Name, now, math.NaN())
				if s.Good {
					changes = append(changes, Change{Time: now, Tag: t, Old: s.raw, New: s.raw, Good: false, Address: s.Address})
				}
				s.Good, s.Error = false, err.Error()
				continue
			}
			s.hist.add(Sample{ms, toFloat(v)})
			m.record(t.Name, now, toFloat(v))
			wasGood := s.Good
			if !t.Changed(s.raw, v) && wasGood {
				s.Updated = now
				continue
			}
			changes = append(changes, Change{Time: now, Tag: t, Old: s.raw, New: v, Good: true, Address: s.Address})
			s.raw, s.Value, s.Display, s.Good, s.Error = v, jsonSafe(v), FormatValue(v), true, ""
			s.Updated, s.Changed = now, now
			s.Changes++
		}
	}
	m.mu.Unlock()
	m.emit(changes)
}

func (m *Monitor) setDisconnected(err error) {
	var changes []Change
	now := time.Now()
	m.mu.Lock()
	m.status.Connected, m.status.LastError = false, err.Error()
	for i := range m.states {
		s := &m.states[i]
		s.hist.add(Sample{now.UnixMilli(), math.NaN()})
		m.record(s.Name, now, math.NaN())
		if s.Good {
			s.Good = false
			changes = append(changes, Change{Time: now, Tag: &m.cfg.Tags[i], Old: s.raw, New: s.raw, Good: false, Address: s.Address})
		}
	}
	m.mu.Unlock()
	m.emit(changes)
}

func (m *Monitor) emit(changes []Change) {
	for _, c := range changes {
		if c.Good && c.Old != nil && !c.Tag.Changed(c.Old, c.New) {
			log.Printf("GOOD   %-20s %-14s %s (quality restored)", c.Tag.Name, c.Address, FormatValue(c.New))
		} else if c.Good {
			log.Printf("CHANGE %-20s %-14s %s -> %s", c.Tag.Name, c.Address, FormatValue(c.Old), FormatValue(c.New))
		}
	}
	if len(changes) > 0 && m.sinks != nil {
		m.sinks.Send(changes)
	}
}

// wait sleeps for d, returning early (true) if the config changes so a new
// network setting is tried at once. It returns false when ctx is cancelled.
func (m *Monitor) wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-m.reload:
		return true
	case <-t.C:
		return true
	}
}

func (m *Monitor) record(tag string, t time.Time, v float64) {
	if m.Trend != nil {
		m.Trend.Record(tag, t, v)
	}
}

// jsonSafe replaces NaN/Inf, which encoding/json rejects, with nil.
func jsonSafe(v any) any {
	if f, ok := v.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
		return nil
	}
	return v
}
