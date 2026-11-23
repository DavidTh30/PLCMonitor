package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robinson/gos7"
	"github.com/simonvetter/modbus"
)

// The network scan finds devices on an IPv4 subnet by trying the TCP ports
// industrial devices listen on, then asks S7 PLCs and Modbus devices who
// they are. It only connects and reads; it never writes.

// Well-known ports and what they mean.
var scanPorts = []struct {
	port int
	name string
}{
	{102, "ISO-TCP"},
	{502, "Modbus TCP"},
	{44818, "EtherNet/IP"},
	{4840, "OPC UA"},
	{80, "Web"},
	{443, "Web (HTTPS)"},
}

const maxScanHosts = 1024 // a /22

type ScanOptions struct {
	Subnet    string `json:"subnet"`     // CIDRs (192.168.0.0/24) or addresses, comma separated, or "all"
	TimeoutMs int    `json:"timeout_ms"` // per connection attempt
	Units     bool   `json:"units"`      // look for Modbus slave IDs behind each Modbus device
	Local     bool   `json:"local"`      // also this PC itself (127.0.0.1): virtual PLCs, simulators
}

type ScanDevice struct {
	IP      string       `json:"ip"`
	Name    string       `json:"name,omitempty"` // reverse DNS
	Ports   []int        `json:"ports"`
	Kind    string       `json:"kind"` // s7, modbus, ethernetip, opcua, web, other
	S7      *S7Ident     `json:"s7,omitempty"`
	Modbus  *ModbusIdent `json:"modbus,omitempty"`
	OPCUA   *UAIdent     `json:"opcua,omitempty"` // any device with port 4840
	Current bool         `json:"current"`         // the device PLC Monitor is connected to
	Network string       `json:"network"`         // the scanned network it was found in
	Self    bool         `json:"self,omitempty"`  // an address of this PC (a program running here)
}

// UAIdent is what an OPC UA server tells about itself (reading only).
type UAIdent struct {
	Endpoint  string    `json:"endpoint"`
	Security  []string  `json:"security,omitempty"` // "None", "Basic256Sha256 sign & encrypt", …
	Anonymous bool      `json:"anonymous"`          // guest access allowed
	Device    string    `json:"device,omitempty"`   // the device object's name, e.g. PLC_1
	Model     string    `json:"model,omitempty"`
	Order     string    `json:"order_number,omitempty"`
	Firmware  string    `json:"firmware,omitempty"`
	Serial    string    `json:"serial,omitempty"`
	Tags      string    `json:"tags,omitempty"` // "yes": publishes PLC tags, "no": the PLC publishes none, "": not known
	Clock     *PLCClock `json:"clock,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type S7Ident struct {
	Rack      int       `json:"rack"`
	Slot      int       `json:"slot"`
	Module    string    `json:"module,omitempty"`     // e.g. "CPU 1214C DC/DC/DC"
	OrderCode string    `json:"order_code,omitempty"` // e.g. "6ES7 214-1AG40-0XB0"
	Firmware  string    `json:"firmware,omitempty"`
	Serial    string    `json:"serial,omitempty"`
	Name      string    `json:"name,omitempty"` // station / module name
	State     string    `json:"state,omitempty"`
	Error     string    `json:"error,omitempty"`
	Note      string    `json:"note,omitempty"`
	Clock     *PLCClock `json:"clock,omitempty"`
}

type ModbusIdent struct {
	Units    []int  `json:"units"`               // slave IDs that answered
	AllUnits bool   `json:"all_units,omitempty"` // answers on any ID: a single device, not a gateway
	Error    string `json:"error,omitempty"`
}

// Scanner runs one network scan and one bus scan at a time in the background.
type Scanner struct {
	mon *Monitor

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	subnet  string
	nets    []string // the networks scanned (entries of subnet, expanded)
	// configured is the address from Network settings that an "all" scan
	// added (it may be outside this PC's networks).
	configured string
	total      int
	done       int
	devices    []ScanDevice
	started    time.Time
	finished   time.Time
	err        string

	bus BusScan

	// ports can be changed by tests to scan servers on other ports.
	s7Port, modbusPort int
}

// BusScan is a slave-ID scan of the current Modbus connection (an RS485
// line, a gateway or a Modbus TCP device).
type BusScan struct {
	Running bool   `json:"running"`
	From    int    `json:"from"`
	To      int    `json:"to"`
	Next    int    `json:"next"`
	Units   []int  `json:"units"`
	Target  string `json:"target"`
	Error   string `json:"error,omitempty"`
	cancel  context.CancelFunc
}

func NewScanner(m *Monitor) *Scanner { return &Scanner{mon: m, s7Port: 102, modbusPort: 502} }

type ScanState struct {
	Running  bool     `json:"running"`
	Subnet   string   `json:"subnet"`
	Networks []string `json:"networks"` // what was scanned, in order (devices carry their network)
	// Configured: the PLC address from Network settings, added to an "all" scan.
	Configured string       `json:"configured,omitempty"`
	Total      int          `json:"total"`
	Done       int          `json:"done"`
	Devices    []ScanDevice `json:"devices"`
	Started    time.Time    `json:"started"`
	Finished   time.Time    `json:"finished"`
	Error      string       `json:"error,omitempty"`
	Bus        BusScan      `json:"bus"`
}

func (s *Scanner) State() ScanState {
	s.mu.Lock()
	defer s.mu.Unlock()
	devs := append([]ScanDevice{}, s.devices...) // [] rather than null before the first scan
	cur := currentHost(s.mon.Config())
	for i := range devs {
		devs[i].Current = devs[i].IP == cur
	}
	bus := s.bus
	bus.Units = append([]int{}, s.bus.Units...)
	return ScanState{Running: s.running, Subnet: s.subnet, Networks: append([]string{}, s.nets...), Configured: s.configured, Total: s.total, Done: s.done, Devices: devs,
		Started: s.started, Finished: s.finished, Error: s.err, Bus: bus}
}

// currentHost is the IP of the device PLC Monitor talks to over the network.
func currentHost(c *Config) string {
	addr := c.PLC.Address
	if c.Protocol == ProtoModbus {
		if c.Modbus.Mode == "rtu" {
			return ""
		}
		addr = c.Modbus.Address
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// hostsOf expands a CIDR or single IPv4 address to the addresses to scan.
func hostsOf(subnet string) ([]net.IP, error) {
	subnet = strings.TrimSpace(subnet)
	if ip := net.ParseIP(subnet); ip != nil && ip.To4() != nil {
		return []net.IP{ip.To4()}, nil
	}
	_, n, err := net.ParseCIDR(subnet)
	if err != nil || n.IP.To4() == nil {
		return nil, fmt.Errorf("%q is not an IPv4 network like 192.168.0.0/24 or a single address", subnet)
	}
	ones, bits := n.Mask.Size()
	size := 1 << (bits - ones)
	if size > maxScanHosts {
		return nil, fmt.Errorf("/%d is %d addresses; scan at most /22 (%d) at a time", ones, size, maxScanHosts)
	}
	base := binary.BigEndian.Uint32(n.IP.To4())
	var ips []net.IP
	for i := 0; i < size; i++ {
		if size > 2 && (i == 0 || i == size-1) {
			continue // network and broadcast address
		}
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, base+uint32(i))
		ips = append(ips, ip)
	}
	return ips, nil
}

// localNetName is the network entry for programs on this PC (virtual PLCs,
// simulators, gateways running here).
const localNetName = "127.0.0.1"

type scanTarget struct {
	ip  net.IP
	net string // the entry it belongs to: a CIDR, an address, or localNetName
}

// scanTargets expands the networks to scan: "all" (every network of this PC
// that has a link), or a list of CIDRs and addresses separated by commas or
// spaces; local adds this PC itself (127.0.0.1). Each address once.
func scanTargets(subnets string, local bool) ([]scanTarget, []string, error) {
	var entries []string
	for _, f := range strings.FieldsFunc(subnets, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if strings.EqualFold(f, "all") {
			for _, n := range LocalNetworks() {
				entries = append(entries, n.Subnet)
			}
			continue
		}
		entries = append(entries, f)
	}
	if local {
		entries = append(entries, localNetName)
	}
	if len(entries) == 0 {
		return nil, nil, errors.New("give a network to scan (e.g. 192.168.0.0/24), \"all\", or tick This PC")
	}
	var out []scanTarget
	var nets []string
	seenNet, seenIP := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		if seenNet[e] {
			continue
		}
		seenNet[e] = true
		hosts, err := hostsOf(e)
		if err != nil {
			return nil, nil, err
		}
		nets = append(nets, e)
		for _, ip := range hosts {
			if k := ip.String(); !seenIP[k] {
				seenIP[k] = true
				out = append(out, scanTarget{ip, e})
			}
		}
		if len(out) > 4*maxScanHosts {
			return nil, nil, fmt.Errorf("more than %d addresses; scan fewer networks at a time", 4*maxScanHosts)
		}
	}
	return out, nets, nil
}

// Start begins a network scan in the background.
func (s *Scanner) Start(opt ScanOptions) error {
	targets, nets, err := scanTargets(opt.Subnet, opt.Local)
	if err != nil {
		return err
	}
	all := strings.EqualFold(strings.TrimSpace(opt.Subnet), "all")
	// "all" also looks at the device PLC Monitor is set up to talk to, even
	// outside this PC's networks: the tree then shows whether it is reachable.
	configured := ""
	if c := s.mon.Config(); all && !c.Simulate {
		if ip := net.ParseIP(currentHost(c)); ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			configured = ip.String()
			have := false
			for _, tg := range targets {
				have = have || tg.ip.Equal(ip)
			}
			if !have { // first: it is what the user is looking for
				targets = append([]scanTarget{{ip.To4(), configured}}, targets...)
				nets = append([]string{configured}, nets...)
			}
		}
	}
	if all && len(LocalNetworks()) == 0 && !opt.Local && configured == "" {
		return errors.New("this PC has no network with a link to scan (cable unplugged? Wi-Fi off?)")
	}
	timeout := time.Duration(opt.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 400 * time.Millisecond
	}
	if timeout < 50*time.Millisecond || timeout > 5*time.Second {
		return errors.New("timeout must be 50..5000 ms")
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("a scan is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.running, s.cancel, s.subnet, s.nets, s.total, s.done = true, cancel, opt.Subnet, nets, len(targets), 0
	s.configured = configured
	s.devices, s.started, s.finished, s.err = nil, time.Now(), time.Time{}, ""
	s7Port, mbPort := s.s7Port, s.modbusPort
	s.mu.Unlock()
	log.Printf("SCAN %s: %d address(es) in %v", opt.Subnet, len(targets), nets)
	own := map[string]bool{"127.0.0.1": true}
	for _, n := range LocalNetworksAll() {
		own[n.IP] = true
	}

	go func() {
		defer cancel()
		sem := make(chan struct{}, 64) // hosts probed at the same time
		var wg sync.WaitGroup
		for _, tg := range targets {
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(tg scanTarget) {
				defer func() { <-sem; wg.Done() }()
				dev, found := probeHost(ctx, tg.ip.String(), timeout, opt.Units, s7Port, mbPort)
				dev.Network, dev.Self = tg.net, own[dev.IP]
				s.mu.Lock()
				s.done++
				if found {
					s.devices = append(s.devices, dev)
					sort.Slice(s.devices, func(i, j int) bool {
						return binary.BigEndian.Uint32(net.ParseIP(s.devices[i].IP).To4()) < binary.BigEndian.Uint32(net.ParseIP(s.devices[j].IP).To4())
					})
				}
				s.mu.Unlock()
			}(tg)
		}
		wg.Wait()
		s.mu.Lock()
		s.running, s.finished = false, time.Now()
		if ctx.Err() != nil {
			s.err = "stopped"
		}
		n := len(s.devices)
		s.mu.Unlock()
		log.Printf("SCAN %s: done, %d device(s) found", opt.Subnet, n)
	}()
	return nil
}

func (s *Scanner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if s.bus.cancel != nil {
		s.bus.cancel()
	}
}

// probeHost checks the well-known ports of one address and identifies what
// answers.
func probeHost(ctx context.Context, ip string, timeout time.Duration, units bool, s7Port, mbPort int) (ScanDevice, bool) {
	dev := ScanDevice{IP: ip}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range scanPorts {
		port := p.port
		switch port {
		case 102:
			port = s7Port
		case 502:
			port = mbPort
		}
		wg.Add(1)
		go func(port, wellKnown int) {
			defer wg.Done()
			d := net.Dialer{Timeout: timeout}
			c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
			if err != nil {
				return
			}
			c.Close()
			mu.Lock()
			dev.Ports = append(dev.Ports, wellKnown)
			mu.Unlock()
		}(port, p.port)
	}
	wg.Wait()
	if len(dev.Ports) == 0 || ctx.Err() != nil {
		return dev, false
	}
	sort.Ints(dev.Ports)
	has := func(p int) bool {
		for _, x := range dev.Ports {
			if x == p {
				return true
			}
		}
		return false
	}

	lctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	if names, err := net.DefaultResolver.LookupAddr(lctx, ip); err == nil && len(names) > 0 {
		dev.Name = strings.TrimSuffix(names[0], ".")
	}
	cancel()

	switch {
	case has(102):
		dev.Kind = "s7"
		dev.S7 = identifyS7(ctx, ip, s7Port)
	case has(502):
		dev.Kind = "modbus"
	case has(44818):
		dev.Kind = "ethernetip"
	case has(4840):
		dev.Kind = "opcua"
	case has(80), has(443):
		dev.Kind = "web"
	default:
		dev.Kind = "other"
	}
	if has(502) {
		dev.Modbus = identifyModbus(ctx, ip, mbPort, units)
	}
	if has(4840) {
		dev.OPCUA = identifyUA(ctx, "opc.tcp://"+net.JoinHostPort(ip, "4840"), timeout)
		// A CPU that does not give its identity over ISO-TCP may over OPC UA.
		if s, u := dev.S7, dev.OPCUA; s != nil && u != nil && s.Module == "" && u.Model != "" {
			s.Module, s.OrderCode, s.Firmware, s.Serial = u.Model, u.Order, u.Firmware, u.Serial
			if s.Name == "" {
				s.Name = u.Device
			}
		}
		if s, u := dev.S7, dev.OPCUA; s != nil && u != nil {
			if s.Firmware == "" {
				s.Firmware = u.Firmware
			}
			if s.Clock == nil {
				s.Clock = u.Clock
			}
		}
	}
	return dev, true
}

// identifyS7 connects with the usual rack/slot combinations and reads the
// CPU's identification (SZL) and run state. Reading only.
func identifyS7(ctx context.Context, ip string, port int) *S7Ident {
	var lastErr error
	for _, rs := range [][2]int{{0, 1}, {0, 2}, {0, 0}, {0, 3}} {
		if ctx.Err() != nil {
			return nil
		}
		h := gos7.NewTCPClientHandler(net.JoinHostPort(ip, fmt.Sprint(port)), rs[0], rs[1])
		h.Timeout, h.IdleTimeout = 2*time.Second, 0
		if err := h.Connect(); err != nil {
			lastErr = err
			continue
		}
		id := &S7Ident{Rack: rs[0], Slot: rs[1]}
		cl := gos7.NewClient(h)
		var errs []string
		if info, err := cl.GetCPUInfo(); err == nil {
			id.Module, id.Serial, id.Name = cleanSZL(info.ModuleTypeName), cleanSZL(info.SerialNumber), cleanSZL(info.ModuleName)
			if as := cleanSZL(info.ASName); as != "" && id.Name == "" {
				id.Name = as
			}
		} else {
			errs = append(errs, "CPU info: "+err.Error())
		}
		if oc, err := cl.GetOrderCode(); err == nil {
			id.OrderCode = cleanSZL(oc.Code)
			id.Firmware = fmt.Sprintf("V%d.%d.%d", oc.V1, oc.V2, oc.V3)
		} else {
			errs = append(errs, "order code: "+err.Error())
		}
		if st, err := cl.PLCGetStatus(); err == nil {
			id.State = map[int]string{0x08: "RUN", 0x04: "STOP"}[st]
			if id.State == "" {
				id.State = fmt.Sprintf("unknown (%#x)", st)
			}
		}
		if t, err := readS7Clock(h); err == nil {
			id.Clock = clockOf(t, false)
		}
		if len(errs) == 2 {
			// The CPU won't tell its identity. That is how 1200/1500-series CPUs
			// answer without PUT/GET access – and how some answer with it. A
			// read of one memory byte tells which.
			buf := make([]byte, 1)
			if err := cl.AGReadMB(0, 1, buf); err == nil {
				id.Note = "this CPU does not report its order number over ISO-TCP; reading works (PUT/GET is on)"
			} else {
				id.Error = "connected, but the CPU does not allow reading (1200/1500-series: enable PUT/GET in the CPU properties)"
			}
		}
		h.Close()
		return id
	}
	msg := "port 102 open, but no ISO-TCP PLC connection on rack 0 slot 0..3"
	if lastErr != nil {
		msg += ": " + lastErr.Error()
	}
	return &S7Ident{Error: msg}
}

func cleanSZL(s string) string { return strings.TrimSpace(strings.Trim(s, "\x00 ")) }

// PLCClock is a PLC's clock as read during a scan, and how far it is from
// this PC's clock (seconds, PLC minus PC) – against local time or UTC,
// whichever is closer: a PLC's clock may run on either.
type PLCClock struct {
	Time string  `json:"time"` // as the PLC gives it, e.g. 2026-11-23 07:41:13
	Diff float64 `json:"diff"`
	Ref  string  `json:"ref"` // "local" or "UTC"
}

func clockOf(plc time.Time, utc bool) *PLCClock {
	now := time.Now()
	if utc { // a UTC instant (OPC UA): show it in this PC's time
		t := plc.Local()
		return &PLCClock{Time: t.Format("2006-01-02 15:04:05"), Diff: t.Sub(now).Seconds(), Ref: "UTC"}
	}
	// A wall clock without a time zone (ISO-TCP): compare with both.
	naive := time.Date(plc.Year(), plc.Month(), plc.Day(), plc.Hour(), plc.Minute(), plc.Second(), plc.Nanosecond(), time.UTC)
	local := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
	dl, du := naive.Sub(local).Seconds(), naive.Sub(now.UTC()).Seconds()
	c := &PLCClock{Time: plc.Format("2006-01-02 15:04:05"), Diff: dl, Ref: "local"}
	if math.Abs(du) < math.Abs(dl) {
		c.Diff, c.Ref = du, "UTC"
	}
	return c
}

// readS7Clock reads the CPU's clock with the "read clock" userdata request
// (reading only). The answer holds DATE_AND_TIME in BCD.
func readS7Clock(h *gos7.TCPClientHandler) (time.Time, error) {
	req := []byte{3, 0, 0, 29, 2, 240, 128, 50, 7, 0, 0, 56, 0, 0, 8, 0, 4, 0, 1, 18, 4, 17, 71, 1, 0, 10, 0, 0, 0}
	resp, err := h.Send(req)
	if err != nil {
		return time.Time{}, err
	}
	return parseS7Clock(resp)
}

// parseS7Clock reads the answer to "read clock": return code 0xFF at byte 29,
// DATE_AND_TIME (BCD: year, month, day, hour, minute, second, ms) from byte 35.
func parseS7Clock(resp []byte) (time.Time, error) {
	if len(resp) < 43 || resp[29] != 0xFF {
		return time.Time{}, errors.New("the CPU did not give its clock")
	}
	bcd := func(b byte) (int, bool) { return int(b>>4)*10 + int(b&15), b>>4 < 10 && b&15 < 10 }
	var v [6]int
	for i := range v {
		n, ok := bcd(resp[35+i])
		if !ok {
			return time.Time{}, errors.New("clock: not a BCD date")
		}
		v[i] = n
	}
	ms, _ := bcd(resp[41])
	ms = ms*10 + int(resp[42]>>4)
	year := 2000 + v[0]
	if v[0] >= 90 {
		year = 1900 + v[0]
	}
	if v[1] < 1 || v[1] > 12 || v[2] < 1 || v[2] > 31 || v[3] > 23 || v[4] > 59 || v[5] > 59 {
		return time.Time{}, errors.New("clock: date out of range")
	}
	return time.Date(year, time.Month(v[1]), v[2], v[3], v[4], v[5], ms*int(time.Millisecond), time.UTC), nil
}

// identifyModbus checks that a Modbus TCP server answers and, if units is
// set, which slave IDs answer (for gateways to RS485 lines).
func identifyModbus(ctx context.Context, ip string, port int, units bool) *ModbusIdent {
	id := &ModbusIdent{Units: []int{}}
	cl, err := modbus.NewClient(&modbus.ClientConfiguration{
		URL: "tcp://" + net.JoinHostPort(ip, fmt.Sprint(port)), Timeout: 300 * time.Millisecond,
		Logger: log.New(io.Discard, "", 0),
	})
	if err == nil {
		err = cl.Open()
	}
	if err != nil {
		id.Error = err.Error()
		return id
	}
	defer cl.Close()
	p := &modbusPLC{client: cl, unit: -1}
	probe := func(u int) (bool, error) { return p.ProbeUnit(u) }

	// A plain Modbus TCP device usually answers on any unit ID. If these
	// all answer, it is one device and there is nothing behind it to list.
	all := true
	for _, u := range []int{1, 2, 3, 100, 247} {
		ok, err := probe(u)
		if err != nil {
			id.Error = err.Error()
			return id
		}
		if ok {
			id.Units = append(id.Units, u)
		} else {
			all = false
		}
	}
	if all {
		id.AllUnits, id.Units = true, []int{}
		return id
	}
	if !units {
		return id
	}
	id.Units = []int{}
	for u := 1; u <= 247 && ctx.Err() == nil; u++ {
		ok, err := probe(u)
		if err != nil {
			id.Error = err.Error()
			break
		}
		if ok {
			id.Units = append(id.Units, u)
		}
	}
	return id
}

// StartBus scans slave IDs from..to on the current Modbus connection. Each
// probe runs through the monitor between two polls, so it works on a serial
// RS485 port that is already open for polling.
func (s *Scanner) StartBus(from, to int) error {
	c := s.mon.Config()
	if c.Protocol != ProtoModbus || c.Simulate {
		return errors.New("a slave ID scan needs a Modbus connection (simulator off)")
	}
	if from < 1 || to > 247 || from > to {
		return errors.New("slave IDs must be within 1..247")
	}
	s.mu.Lock()
	if s.bus.Running {
		s.mu.Unlock()
		return errors.New("a slave ID scan is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.bus = BusScan{Running: true, From: from, To: to, Next: from, Units: []int{}, Target: modbusTarget(c.Modbus), cancel: cancel}
	s.mu.Unlock()
	log.Printf("SCAN slave IDs %d..%d on %s", from, to, modbusTarget(c.Modbus))

	go func() {
		defer cancel()
		for u := from; u <= to && ctx.Err() == nil; u++ {
			var found bool
			err := s.mon.Do(func(conn PLC) error {
				p, ok := conn.(*modbusPLC)
				if !ok {
					return errors.New("not a Modbus connection")
				}
				var err error
				found, err = p.ProbeUnit(u)
				return err
			})
			s.mu.Lock()
			s.bus.Next = u + 1
			if err != nil {
				s.bus.Error = err.Error()
				s.mu.Unlock()
				break
			}
			if found {
				s.bus.Units = append(s.bus.Units, u)
			}
			s.mu.Unlock()
		}
		s.mu.Lock()
		s.bus.Running = false
		if ctx.Err() != nil && s.bus.Error == "" {
			s.bus.Error = "stopped"
		}
		log.Printf("SCAN slave IDs: found %v", s.bus.Units)
		s.mu.Unlock()
	}()
	return nil
}

// LocalNetworks lists this computer's IPv4 networks, as suggestions for
// what to scan.
type LocalNetwork struct {
	Interface string `json:"interface"`
	IP        string `json:"ip"`
	Subnet    string `json:"subnet"`
	// Down: the adapter has an address set but no link (cable unplugged,
	// switch off). Listed so the user sees why that network can't be reached.
	Down bool `json:"down,omitempty"`
}

// LocalNetworks lists the networks with a link first; LocalNetworksAll also
// the adapters that have an address but no link.
func LocalNetworks() []LocalNetwork {
	var out []LocalNetwork
	for _, n := range LocalNetworksAll() {
		if !n.Down {
			out = append(out, n)
		}
	}
	return out
}

func LocalNetworksAll() []LocalNetwork {
	var up, down []LocalNetwork
	ifs, _ := net.Interfaces()
	for _, in := range ifs {
		if in.Flags&net.FlagLoopback != 0 {
			continue
		}
		isDown := in.Flags&net.FlagUp == 0 || in.Flags&net.FlagRunning == 0
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || n.IP.IsLinkLocalUnicast() {
				continue
			}
			ones, _ := n.Mask.Size()
			if ones < 22 {
				ones = 24 // big networks: suggest the /24 around this PC
			}
			_, sub, _ := net.ParseCIDR(fmt.Sprintf("%s/%d", n.IP, ones))
			ln := LocalNetwork{Interface: in.Name, IP: n.IP.String(), Subnet: sub.String(), Down: isDown}
			if isDown {
				down = append(down, ln)
			} else {
				up = append(up, ln)
			}
		}
	}
	return append(up, down...)
}
