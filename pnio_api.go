package main

// The PROFINET IO test in the Topology window: one session at a time on this
// computer (not through the Linux agent yet), driven by the page through
// /api/pnio/*.
//
// Safety: outputs start disarmed (IOPS bad → the device's substitute values).
// The page polls the status about 4 times a second; that is the dead-man: no
// poll for 3 s disarms the outputs, no poll for 15 s releases the device.

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// pnioStartReq says what to connect to. From the Topology window MAC, name
// and IDs come from DCP; the console command finds them by DCP itself.
type pnioStartReq struct {
	Adapter  string `json:"adapter"` // pcap ID or Windows name; "" = the one in the device's subnet
	IP       string `json:"ip"`
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	VendorID uint16 `json:"vendor_id"`
	DeviceID uint16 `json:"device_id"`
	Slots    string `json:"slots"` // "" = p-net sample (DI 8 in slot 1, DO 8 in slot 2)
	Ports    int    `json:"ports"`
	DAP      string `json:"dap"` // module/submodule, "" = 0x1/0x1
	CycleMs  int    `json:"cycle_ms"`
	WDF      int    `json:"wdf"`
	LocalIP  string `json:"local_ip"`
}

// pnioPrepare checks the request, finds the adapter, this PC's address in the
// device's subnet and (without a MAC) the device by DCP, and opens the UDP
// socket and the raw link. The caller owns conn and link afterwards.
func pnioPrepare(q pnioStartReq, logf func(string, ...any)) (pnioConfig, *net.UDPConn, dcpLink, error) {
	var cfg pnioConfig
	if dcpListAdapter == nil {
		return cfg, nil, nil, errors.New(dcpUnavailable)
	}
	devIP := net.ParseIP(strings.TrimSpace(q.IP)).To4()
	if devIP == nil {
		return cfg, nil, nil, errors.New("the device needs an IPv4 address for the IO test (Set IP / name…)")
	}
	if q.CycleMs == 0 {
		q.CycleMs = 32
	}
	if q.CycleMs < 1 || q.CycleMs > 512 || q.CycleMs&(q.CycleMs-1) != 0 {
		return cfg, nil, nil, errors.New("the cycle time must be a power of two from 1 to 512 ms")
	}
	if q.WDF == 0 {
		q.WDF = 10
	}
	if q.Ports <= 0 {
		q.Ports = 1
	}
	if q.Ports > 8 {
		return cfg, nil, nil, errors.New("at most 8 ports")
	}
	if q.DAP == "" {
		q.DAP = "0x1/0x1"
	}

	adapters, err := dcpListAdapter()
	if err != nil {
		return cfg, nil, nil, err
	}
	sameNet := func(ip string) bool {
		a := net.ParseIP(ip).To4()
		return a != nil && a[0] == devIP[0] && a[1] == devIP[1] && a[2] == devIP[2]
	}
	var ad *DCPAdapter
	for i := range adapters {
		a := &adapters[i]
		if q.Adapter != "" && (a.ID == q.Adapter || strings.EqualFold(a.Name, q.Adapter)) {
			ad = a
		}
		if q.Adapter == "" {
			for _, ip := range a.IPs {
				if sameNet(ip) {
					ad = a
				}
			}
		}
	}
	if ad == nil {
		var names []string
		for _, a := range adapters {
			names = append(names, fmt.Sprintf("%q %v", a.Name, a.IPs))
		}
		return cfg, nil, nil, fmt.Errorf("no network adapter of this PC is in %s's subnet; adapters: %s", devIP, strings.Join(names, ", "))
	}
	local := net.ParseIP(q.LocalIP).To4()
	for _, ip := range ad.IPs {
		if local == nil && sameNet(ip) {
			local = net.ParseIP(ip).To4()
		}
	}
	if local == nil {
		return cfg, nil, nil, fmt.Errorf("%s has no IPv4 address in %s's subnet: give it one first (Network adapters…)", ad.Name, devIP)
	}

	cfg = pnioConfig{DeviceIP: devIP, SendClockFactor: 32, ReductionRatio: uint16(q.CycleMs), WatchdogFactor: uint16(q.WDF),
		StationName: q.Name, VendorID: q.VendorID, DeviceID: q.DeviceID}
	if q.MAC != "" {
		if cfg.DeviceMAC, err = net.ParseMAC(q.MAC); err != nil || len(cfg.DeviceMAC) != 6 {
			return cfg, nil, nil, fmt.Errorf("bad MAC address %q", q.MAC)
		}
	} else {
		logf("looking for %s with DCP on %s …", devIP, ad.Name)
		devs, err := dcpIdentify(ad.ID, 2*time.Second)
		if err != nil {
			return cfg, nil, nil, err
		}
		for _, d := range devs {
			if d.IP == devIP.String() {
				cfg.DeviceMAC, _ = net.ParseMAC(d.MAC)
				cfg.StationName, cfg.VendorID, cfg.DeviceID = d.Name, d.VendorID, d.DeviceID
				logf("found %q (%s), %s, vendor 0x%04X device 0x%04X", d.Name, d.MAC, d.TypeOfStation, d.VendorID, d.DeviceID)
			}
		}
		if cfg.DeviceMAC == nil {
			return cfg, nil, nil, fmt.Errorf("no PROFINET device with IP %s answered DCP on %s", devIP, ad.Name)
		}
	}

	dap := strings.Split(q.DAP, "/")
	if len(dap) != 2 {
		return cfg, nil, nil, errors.New("DAP: want module/submodule ident numbers, e.g. 0x1/0x1")
	}
	dapMod, err1 := parseNum(dap[0])
	dapSub, err2 := parseNum(dap[1])
	if err1 != nil || err2 != nil {
		return cfg, nil, nil, errors.New("DAP: bad number")
	}
	cfg.Slots = pnetSampleSlots(q.Ports)
	cfg.Slots[0].Module, cfg.Slots[0].Subs[0].Ident = uint32(dapMod), uint32(dapSub)
	if strings.TrimSpace(q.Slots) != "" {
		more, err := parsePNIOSlots(q.Slots)
		if err != nil {
			return cfg, nil, nil, fmt.Errorf("modules: %v", err)
		}
		cfg.Slots = append(cfg.Slots[:1], more...)
	}
	if err := cfg.validate(); err != nil {
		return cfg, nil, nil, err
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: local, Port: pnioRPCPort})
	if err != nil {
		return cfg, nil, nil, fmt.Errorf("UDP port %d on %s is in use (%v): close PRONETA, TIA Portal or other PROFINET tools", pnioRPCPort, local, err)
	}
	link, err := pnioOpenLink(ad.ID, cfg.DeviceMAC)
	if err != nil {
		conn.Close()
		return cfg, nil, nil, err
	}
	logf("connecting as IO controller from %s (%s), cycle %d ms, watchdog %v", local, ad.Name, q.CycleMs, time.Duration(q.WDF)*cfg.cycle())
	return cfg, conn, link, nil
}

// ---- the one session of this computer ----

type pnioEvent struct {
	N    int       `json:"n"`
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

type pnioManager struct {
	mu       sync.Mutex
	sess     *pnioSession
	busy     string // "connecting" / "stopping" while that runs
	err      string // why the last start failed
	device   pnioStartReq
	events   []pnioEvent
	seq      int
	lastPoll time.Time
	stopReq  bool // Stop came while connecting: release as soon as connected
}

var (
	pnioMgr       = &pnioManager{}
	pnioWatchOnce sync.Once
)

func (m *pnioManager) event(format string, a ...any) {
	text := fmt.Sprintf(format, a...)
	m.mu.Lock()
	m.seq++
	m.events = append(m.events, pnioEvent{m.seq, time.Now(), text})
	if len(m.events) > 300 {
		m.events = m.events[len(m.events)-300:]
	}
	m.mu.Unlock()
}

func (m *pnioManager) start(q pnioStartReq) error {
	m.mu.Lock()
	if m.sess != nil || m.busy != "" {
		m.mu.Unlock()
		return errors.New("an IO test is already running: stop it first")
	}
	m.busy, m.err, m.device, m.lastPoll, m.stopReq = "connecting", "", q, time.Now(), false
	m.events, m.seq = nil, 0
	m.mu.Unlock()
	go func() {
		cfg, conn, link, err := pnioPrepare(q, m.event)
		var s *pnioSession
		if err == nil {
			cfg.OnEvent = func(e string) { m.event("%s", e) }
			s, err = startPNIO(cfg, link, conn)
		}
		m.mu.Lock()
		m.busy = ""
		cancel := m.stopReq
		if err != nil {
			m.err = err.Error()
		} else if !cancel {
			m.sess = s
		}
		m.mu.Unlock()
		if err == nil && cancel {
			m.event("stopped while connecting: releasing the device")
			s.Close()
			return
		}
		if err != nil {
			m.event("not connected: %v", err)
			log.Printf("PROFINET IO test %s (%s): not connected: %v", q.IP, q.MAC, err)
		} else {
			log.Printf("PROFINET IO test %s (%s): connected", q.IP, q.MAC)
		}
	}()
	return nil
}

func (m *pnioManager) stop() {
	m.mu.Lock()
	s := m.sess
	m.sess = nil
	if s != nil {
		m.busy = "stopping"
	} else if m.busy == "connecting" {
		m.stopReq = true
	}
	m.mu.Unlock()
	if s == nil {
		return
	}
	go func() {
		s.Close()
		m.mu.Lock()
		m.busy = ""
		m.mu.Unlock()
		log.Printf("PROFINET IO test %s: stopped", m.device.IP)
	}()
}

// watch is the dead-man: it runs as long as the program does.
func (m *pnioManager) watch() {
	for range time.Tick(500 * time.Millisecond) {
		m.check()
	}
}

// check disarms after 3 s and releases after 15 s without a status poll.
func (m *pnioManager) check() {
	m.mu.Lock()
	s, idle := m.sess, time.Since(m.lastPoll)
	m.mu.Unlock()
	if s == nil {
		return
	}
	if idle > 15*time.Second {
		m.event("the IO test window is gone: releasing the device")
		m.stop()
		return
	}
	if idle > 3*time.Second && s.Snapshot().Armed {
		s.Arm(false)
		m.event("outputs disarmed: the IO test window stopped answering")
	}
}

type pnioIOJSON struct {
	Slot    uint16 `json:"slot"`
	Subslot uint16 `json:"subslot"`
	Data    []int  `json:"data"`
	IOPS    byte   `json:"iops"`
	IOCS    byte   `json:"iocs"`
}

func ioJSON(v []pnioValue) []pnioIOJSON {
	out := []pnioIOJSON{}
	for _, x := range v {
		d := make([]int, len(x.Data))
		for i, b := range x.Data {
			d[i] = int(b)
		}
		out = append(out, pnioIOJSON{x.Slot, x.Subslot, d, x.IOPS, x.IOCS})
	}
	return out
}

func (m *pnioManager) status(since int) map[string]any {
	m.mu.Lock()
	m.lastPoll = time.Now()
	s := m.sess
	res := map[string]any{"device": m.device, "error": m.err, "busy": m.busy}
	var ev []pnioEvent
	for _, e := range m.events {
		if e.N > since {
			ev = append(ev, e)
		}
	}
	res["events"] = ev
	m.mu.Unlock()
	res["running"] = s != nil
	if s != nil {
		sn := s.Snapshot()
		res["state"] = sn.State
		res["armed"] = sn.Armed
		res["inputs"] = ioJSON(sn.Inputs)
		res["outputs"] = ioJSON(sn.Outputs)
		res["data_status"] = dataStatusText(sn.InStatus)
		res["in_frames"] = sn.InCount
		res["out_frames"] = sn.Sent
		res["late"] = sn.Late
		res["max_late_ms"] = float64(sn.MaxLate.Microseconds()) / 1000
		if !sn.LastIn.IsZero() {
			res["last_in_ms"] = time.Since(sn.LastIn).Milliseconds()
		}
		res["cycle_ms"] = s.cfg.cycle().Milliseconds()
	}
	return res
}

// ---- firewall (Windows): let the device's ApplicationReady in ----

const pnioFirewallRule = "PLC Monitor PROFINET IO"

// pnioFirewall tells whether the inbound rule for UDP 34964 exists: "yes",
// "no", or "" (not Windows / unknown).
func pnioFirewall() string {
	if runElevated == nil {
		return ""
	}
	c := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+pnioFirewallRule)
	hideCmd(c)
	out, err := c.Output()
	if err != nil {
		return "no" // netsh exits with 1 when no rule has that name
	}
	if strings.Contains(string(out), "34964") {
		return "yes"
	}
	return "no"
}

func pnioAllowFirewall() error {
	if runElevated == nil {
		return errors.New("only on Windows")
	}
	return runElevated("if (-not (Get-NetFirewallRule -DisplayName '" + pnioFirewallRule + "' -ErrorAction SilentlyContinue)) {\n" +
		"  New-NetFirewallRule -DisplayName '" + pnioFirewallRule + "' -Description 'PROFINET IO test: the device answers on UDP 34964' " +
		"-Direction Inbound -Protocol UDP -LocalPort 34964 -Action Allow -Profile Any | Out-Null\n}")
}

// ---- HTTP ----

// pnioRoutes: reading needs this computer; changing also a dashboard that is
// not read-only. change also checks the JSON content type (no plain forms).
func pnioRoutes(mux *http.ServeMux, readOnly bool, change func(http.ResponseWriter, *http.Request) bool) {
	pnioWatchOnce.Do(func() { go pnioMgr.watch() })
	here := func(w http.ResponseWriter, r *http.Request) bool {
		if !isLoopback(r) {
			writeJSON(w, http.StatusForbidden, apiError{"the IO test only works on the computer running PLC Monitor"})
			return false
		}
		return true
	}
	act := func(w http.ResponseWriter, r *http.Request) bool {
		if readOnly {
			writeJSON(w, http.StatusForbidden, apiError{"read-only dashboard"})
			return false
		}
		return change(w, r)
	}
	mux.HandleFunc("GET /api/pnio/status", func(w http.ResponseWriter, r *http.Request) {
		if !here(w, r) {
			return
		}
		var since int
		fmt.Sscan(r.URL.Query().Get("since"), &since)
		res := pnioMgr.status(since)
		res["available"] = rtOpen != nil || rawOpen != nil
		res["unavailable"] = ""
		if rtOpen == nil && rawOpen == nil {
			res["unavailable"] = dcpUnavailable
		}
		res["read_only"] = readOnly
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/pnio/firewall", func(w http.ResponseWriter, r *http.Request) {
		if here(w, r) {
			writeJSON(w, http.StatusOK, map[string]string{"rule": pnioFirewall()})
		}
	})
	mux.HandleFunc("POST /api/pnio/firewall", func(w http.ResponseWriter, r *http.Request) {
		if !act(w, r) {
			return
		}
		err := pnioAllowFirewall()
		log.Printf("PROFINET IO test: firewall rule for UDP 34964 – %v", map[bool]string{true: "added", false: fmt.Sprint(err)}[err == nil])
		respond(w, err)
	})
	mux.HandleFunc("POST /api/pnio/start", func(w http.ResponseWriter, r *http.Request) {
		if !act(w, r) {
			return
		}
		var q pnioStartReq
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		log.Printf("PROFINET IO test: connect to %s %s (%s) from %s", q.IP, q.MAC, q.Name, r.RemoteAddr)
		respond(w, pnioMgr.start(q))
	})
	mux.HandleFunc("POST /api/pnio/stop", func(w http.ResponseWriter, r *http.Request) {
		if act(w, r) {
			pnioMgr.stop()
			respond(w, nil)
		}
	})
	mux.HandleFunc("POST /api/pnio/arm", func(w http.ResponseWriter, r *http.Request) {
		if !act(w, r) {
			return
		}
		var body struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		pnioMgr.mu.Lock()
		s := pnioMgr.sess
		pnioMgr.mu.Unlock()
		if s == nil {
			respond(w, errors.New("no IO test is running"))
			return
		}
		s.Arm(body.On)
		log.Printf("PROFINET IO test %s: outputs %s (from %s)", pnioMgr.device.IP, map[bool]string{true: "ARMED", false: "disarmed"}[body.On], r.RemoteAddr)
		respond(w, nil)
	})
	mux.HandleFunc("POST /api/pnio/output", func(w http.ResponseWriter, r *http.Request) {
		if !act(w, r) {
			return
		}
		var body struct {
			Slot    uint16 `json:"slot"`
			Subslot uint16 `json:"subslot"`
			Hex     string `json:"hex"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return
		}
		data, err := hex.DecodeString(body.Hex)
		if err != nil {
			respond(w, fmt.Errorf("bad data %q", body.Hex))
			return
		}
		pnioMgr.mu.Lock()
		s := pnioMgr.sess
		pnioMgr.mu.Unlock()
		if s == nil {
			respond(w, errors.New("no IO test is running"))
			return
		}
		if body.Subslot == 0 {
			body.Subslot = 1
		}
		err = s.SetOutput(body.Slot, body.Subslot, data)
		if err == nil {
			log.Printf("PROFINET IO test %s: output slot %d.%d = %s", pnioMgr.device.IP, body.Slot, body.Subslot, strings.ToUpper(body.Hex))
		}
		respond(w, err)
	})
}
