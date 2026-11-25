package main

// PROFINET IO controller for the IO test – Phase 0 spike.
//
// One application relation (AR) to one device, RT_CLASS_1, a fixed module
// configuration (no GSDML yet), legacy startup:
//
//	Connect → cyclic frames start → PrmEnd → device sends ApplicationReady →
//	data exchange → Release.
//
// Outputs are sent with IOPS = BAD (device keeps its substitute values) until
// the user arms them. See docs/profinet-io-test.md.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"
)

// pnioSub is one submodule with its IO data lengths in bytes (both 0 = NO_IO).
type pnioSub struct {
	Subslot uint16
	Ident   uint32
	In, Out int
}

type pnioSlot struct {
	Slot   uint16
	Module uint32
	Subs   []pnioSub
}

type pnioConfig struct {
	DeviceIP           net.IP
	DevicePort         int // 34964; tests use another port
	DeviceMAC          net.HardwareAddr
	StationName        string
	VendorID, DeviceID uint16
	Slots              []pnioSlot
	SendClockFactor    uint16 // 32 = 1 ms
	ReductionRatio     uint16
	WatchdogFactor     uint16
	ConnectTimeout     time.Duration // wait for ApplicationReady after PrmEnd
	OnEvent            func(string)  // state changes, alarms, errors (else Events())
}

// pnetSampleSlots is the p-net sample app: DAP (with interface and ports),
// slot 1 "DI 8xLogicLevel", slot 2 "DO 8xLogicLevel".
func pnetSampleSlots(ports int) []pnioSlot {
	dap := pnioSlot{Slot: 0, Module: 0x00000001, Subs: []pnioSub{{Subslot: 1, Ident: 0x00000001}, {Subslot: 0x8000, Ident: 0x00008000}}}
	for p := 1; p <= ports; p++ {
		dap.Subs = append(dap.Subs, pnioSub{Subslot: uint16(0x8000 + p), Ident: uint32(0x8000 + p)})
	}
	return []pnioSlot{
		dap,
		{Slot: 1, Module: 0x00000030, Subs: []pnioSub{{Subslot: 1, Ident: 0x00000130, In: 1}}},
		{Slot: 2, Module: 0x00000031, Subs: []pnioSub{{Subslot: 1, Ident: 0x00000131, Out: 1}}},
	}
}

// parsePNIOSlots reads "slot:module/submodule:i1o1,…" (numbers may be hex
// with 0x). Subslot is 1. The DAP (slot 0) is added by the caller.
func parsePNIOSlots(spec string) ([]pnioSlot, error) {
	var out []pnioSlot
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		f := strings.Split(item, ":")
		if len(f) != 3 {
			return nil, fmt.Errorf("%q: want slot:module/submodule:i<bytes>o<bytes>", item)
		}
		var slot, mod, sub uint64
		if _, err := fmt.Sscan(f[0], &slot); err != nil || slot == 0 {
			return nil, fmt.Errorf("%q: bad slot", item)
		}
		ids := strings.Split(f[1], "/")
		if len(ids) != 2 {
			return nil, fmt.Errorf("%q: want module/submodule ident numbers", item)
		}
		var err error
		if mod, err = parseNum(ids[0]); err != nil {
			return nil, fmt.Errorf("%q: %v", item, err)
		}
		if sub, err = parseNum(ids[1]); err != nil {
			return nil, fmt.Errorf("%q: %v", item, err)
		}
		var in, outLen int
		io := strings.ToLower(f[2])
		for io != "" {
			var n int
			k := io[0]
			rest := io[1:]
			j := 0
			for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
				n = n*10 + int(rest[j]-'0')
				j++
			}
			if j == 0 || (k != 'i' && k != 'o') {
				return nil, fmt.Errorf("%q: IO part must look like i1, o2 or i1o1", item)
			}
			if k == 'i' {
				in = n
			} else {
				outLen = n
			}
			io = rest[j:]
		}
		out = append(out, pnioSlot{Slot: uint16(slot), Module: uint32(mod), Subs: []pnioSub{{Subslot: 1, Ident: uint32(sub), In: in, Out: outLen}}})
	}
	return out, nil
}

func parseNum(s string) (uint64, error) {
	var v uint64
	s = strings.TrimSpace(s)
	var err error
	if strings.HasPrefix(strings.ToLower(s), "0x") {
		_, err = fmt.Sscanf(s[2:], "%x", &v)
	} else {
		_, err = fmt.Sscan(s, &v)
	}
	if err != nil {
		return 0, fmt.Errorf("bad number %q", s)
	}
	return v, nil
}

func (c *pnioConfig) cycle() time.Duration {
	return time.Duration(c.SendClockFactor) * time.Duration(c.ReductionRatio) * 31250 * time.Nanosecond
}

func (c *pnioConfig) validate() error {
	if len(c.DeviceMAC) != 6 || c.DeviceIP.To4() == nil {
		return errors.New("device MAC and IPv4 address are needed")
	}
	if c.SendClockFactor == 0 || c.ReductionRatio == 0 || c.ReductionRatio > 512 {
		return errors.New("bad cycle time")
	}
	if c.WatchdogFactor < 3 {
		return errors.New("watchdog factor must be at least 3")
	}
	if wd := time.Duration(c.WatchdogFactor) * c.cycle(); wd > 1920*time.Millisecond {
		return fmt.Errorf("watchdog time %v is above the 1.92 s limit", wd)
	}
	if len(c.Slots) == 0 || c.Slots[0].Slot != 0 {
		return errors.New("slot 0 (the DAP) must come first")
	}
	return nil
}

// ---- frame layout ----

type pnioPos struct {
	Slot, Subslot uint16
	Off, Len      int // data at Off, its IOPS at Off+Len
}

type pnioIOCSPos struct {
	Slot, Subslot uint16
	Off           int
}

type pnioCRLayout struct {
	Data   []pnioPos
	IOCS   []pnioIOCSPos
	Length int // C_SDU length, at least 40
}

// pnioLayout places the data, IOPS and IOCS of every submodule. Input CR
// (device → us): data+IOPS of input and NO_IO submodules, IOCS of output
// submodules. Output CR: the other way round.
func pnioLayout(slots []pnioSlot) (in, out pnioCRLayout) {
	for _, sl := range slots {
		for _, s := range sl.Subs {
			hasIn := s.In > 0 || s.Out == 0
			if hasIn {
				in.Data = append(in.Data, pnioPos{sl.Slot, s.Subslot, in.Length, s.In})
				in.Length += s.In + 1
			}
			if s.Out > 0 {
				out.Data = append(out.Data, pnioPos{sl.Slot, s.Subslot, out.Length, s.Out})
				out.Length += s.Out + 1
			}
		}
	}
	for _, sl := range slots {
		for _, s := range sl.Subs {
			if s.In > 0 || s.Out == 0 {
				out.IOCS = append(out.IOCS, pnioIOCSPos{sl.Slot, s.Subslot, out.Length})
				out.Length++
			}
			if s.Out > 0 {
				in.IOCS = append(in.IOCS, pnioIOCSPos{sl.Slot, s.Subslot, in.Length})
				in.Length++
			}
		}
	}
	in.Length, out.Length = max(in.Length, 40), max(out.Length, 40)
	return
}

// ---- connect ----

type pnioAR struct {
	UUID                    uuid16
	SessionKey              uint16
	InFrameID, OutFrameID   uint16
	LocalAlarmRef, DevAlarm uint16
	ControllerObject        uuid16
}

const (
	pnioStationName   = "plcmonitor-iotest"
	arPropActive      = 0x00000001
	arPropPrmServerCM = 0x00000010
	iocrInput         = 1
	iocrOutput        = 2
	rtClass1          = 1
)

func be16(b []byte, v uint16) []byte { return binary.BigEndian.AppendUint16(b, v) }
func be32(b []byte, v uint32) []byte { return binary.BigEndian.AppendUint32(b, v) }

func buildConnect(c *pnioConfig, ar *pnioAR, ourMAC net.HardwareAddr, in, out pnioCRLayout) []byte {
	var blocks []byte

	// ARBlockReq: IOCARSingle, supervisor takeover NOT allowed, legacy startup.
	b := be16(nil, 0x0001)
	b = append(b, ar.UUID[:]...)
	b = be16(b, ar.SessionKey)
	b = append(b, ourMAC...)
	b = append(b, ar.ControllerObject[:]...)
	b = be32(b, arPropActive|arPropPrmServerCM)
	b = be16(b, 600) // CM initiator activity timeout: 60 s
	b = be16(b, 0x8892)
	b = be16(b, uint16(len(pnioStationName)))
	b = append(b, pnioStationName...)
	blocks = appendBlock(blocks, 0x0101, b)

	blocks = appendBlock(blocks, 0x0102, iocrBody(c, iocrInput, 1, ar.InFrameID, in))
	blocks = appendBlock(blocks, 0x0102, iocrBody(c, iocrOutput, 2, ar.OutFrameID, out))

	// AlarmCRBlockReq
	b = be16(nil, 1)    // AlarmCRType
	b = be16(b, 0x8892) // LT
	b = be32(b, 0)      // properties: user priority, RTA over layer 2
	b = be16(b, 1)      // RTATimeoutFactor × 100 ms
	b = be16(b, 3)      // RTARetries
	b = be16(b, ar.LocalAlarmRef)
	b = be16(b, 200)    // MaxAlarmDataLength
	b = be16(b, 0xC000) // tag header high: priority 6
	b = be16(b, 0xA000) // tag header low: priority 5
	blocks = appendBlock(blocks, 0x0103, b)

	// ExpectedSubmoduleBlockReq, one per slot.
	for _, sl := range c.Slots {
		b = be16(nil, 1) // NumberOfAPIs
		b = be32(b, 0)   // API 0
		b = be16(b, sl.Slot)
		b = be32(b, sl.Module)
		b = be16(b, 0) // ModuleProperties
		b = be16(b, uint16(len(sl.Subs)))
		for _, s := range sl.Subs {
			b = be16(b, s.Subslot)
			b = be32(b, s.Ident)
			typ := uint16(0) // NO_IO
			switch {
			case s.In > 0 && s.Out > 0:
				typ = 3
			case s.In > 0:
				typ = 1
			case s.Out > 0:
				typ = 2
			}
			b = be16(b, typ)
			desc := func(dir uint16, n int) {
				b = be16(b, dir)
				b = be16(b, uint16(n))
				b = append(b, 1, 1) // LengthIOCS, LengthIOPS
			}
			if typ == 0 || s.In > 0 {
				desc(1, s.In)
			}
			if s.Out > 0 {
				desc(2, s.Out)
			}
		}
		blocks = appendBlock(blocks, 0x0104, b)
	}
	return blocks
}

func iocrBody(c *pnioConfig, typ, ref, frameID uint16, l pnioCRLayout) []byte {
	b := be16(nil, typ)
	b = be16(b, ref)
	b = be16(b, 0x8892)
	b = be32(b, rtClass1)
	b = be16(b, uint16(l.Length))
	b = be16(b, frameID)
	b = be16(b, c.SendClockFactor)
	b = be16(b, c.ReductionRatio)
	b = be16(b, 1)          // phase
	b = be16(b, 0)          // sequence
	b = be32(b, 0xFFFFFFFF) // frame send offset: as soon as possible
	b = be16(b, c.WatchdogFactor)
	b = be16(b, c.WatchdogFactor) // data hold factor
	b = be16(b, 0xC000)           // VLAN tag: priority 6, VLAN 0
	b = append(b, 0, 0, 0, 0, 0, 0)
	b = be16(b, 1) // NumberOfAPIs
	b = be32(b, 0)
	b = be16(b, uint16(len(l.Data)))
	for _, d := range l.Data {
		b = be16(b, d.Slot)
		b = be16(b, d.Subslot)
		b = be16(b, uint16(d.Off))
	}
	b = be16(b, uint16(len(l.IOCS)))
	for _, d := range l.IOCS {
		b = be16(b, d.Slot)
		b = be16(b, d.Subslot)
		b = be16(b, uint16(d.Off))
	}
	return b
}

// parseConnectRes takes the frame IDs and the device's alarm reference from
// the response. It returns the ModuleDiffBlock as text ("" when there is none).
func parseConnectRes(blocks []byte, ar *pnioAR) (string, error) {
	list, err := splitBlocks(blocks)
	if err != nil {
		return "", err
	}
	var diff string
	gotAR := false
	for _, bl := range list {
		r := &beReader{b: bl.Body}
		switch bl.Type {
		case 0x8101: // ARBlockRes
			r.u16()
			if r.uuid() != ar.UUID {
				return "", errors.New("connect response is for another AR")
			}
			gotAR = true
		case 0x8102: // IOCRBlockRes
			typ, _, fid := r.u16(), r.u16(), r.u16()
			if typ == iocrInput {
				ar.InFrameID = fid
			} else if typ == iocrOutput {
				ar.OutFrameID = fid
			}
		case 0x8103: // AlarmCRBlockRes
			r.u16()
			ar.DevAlarm = r.u16()
		case 0x8104:
			diff = moduleDiffText(bl.Body)
		}
		if r.bad {
			return "", fmt.Errorf("connect response: block 0x%04x too short", bl.Type)
		}
	}
	if !gotAR {
		return diff, errors.New("connect response has no ARBlockRes")
	}
	return diff, nil
}

// moduleDiffText describes a ModuleDiffBlock: what the device has where it
// differs from what we expected.
func moduleDiffText(body []byte) string {
	r := &beReader{b: body}
	var lines []string
	modState := map[uint16]string{0: "no module", 1: "wrong module", 2: "proper module", 3: "substitute module"}
	ident := []string{"OK", "substitute", "wrong submodule", "no submodule"}
	for na := r.u16(); na > 0 && !r.bad; na-- {
		r.u32() // API
		for nm := r.u16(); nm > 0 && !r.bad; nm-- {
			slot, mod, st := r.u16(), r.u32(), r.u16()
			lines = append(lines, fmt.Sprintf("slot %d: module 0x%X, %s", slot, mod, modState[st]))
			for ns := r.u16(); ns > 0 && !r.bad; ns-- {
				sub, sid, sst := r.u16(), r.u32(), r.u16()
				t := fmt.Sprintf("0x%04X", sst)
				if sst&0x8000 != 0 {
					t = ident[(sst>>11)&3]
					if (sst>>11)&0xF > 3 {
						t = fmt.Sprintf("ident info %d", (sst>>11)&0xF)
					}
					if ai := (sst >> 7) & 0xF; ai != 0 {
						t += fmt.Sprintf(", AR info %d", ai)
					}
					if sst&0x40 != 0 {
						t += ", diagnosis"
					}
				}
				lines = append(lines, fmt.Sprintf("  subslot 0x%04X: submodule 0x%X, %s", sub, sid, t))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// controlBody is the body of IODControlReq / IOXControlRes / IODReleaseReq.
func controlBody(ar *pnioAR, cmd uint16) []byte {
	b := be16(nil, 0) // reserved
	b = append(b, ar.UUID[:]...)
	b = be16(b, ar.SessionKey)
	b = be16(b, 0) // reserved
	b = be16(b, cmd)
	return be16(b, 0) // ControlBlockProperties
}

const (
	ctlPrmEnd   = 0x0001
	ctlAppReady = 0x0002
	ctlRelease  = 0x0004
	ctlDone     = 0x0008
)

// ---- RPC endpoint (client and the server for ApplicationReady) ----

type rpcIn struct {
	h    rpcHeader
	body []byte
}

type pnioRPC struct {
	conn     *net.UDPConn
	dev      *net.UDPAddr
	object   uuid16
	activity uuid16
	boot     uint32 // the device's server boot time, from its responses
	ourBoot  uint32
	mu       sync.Mutex // one call at a time
	seq      uint32
	replies  chan rpcIn
	handle   func(h rpcHeader, body []byte) []byte // serves requests to us
	closed   chan struct{}
}

func newPNIORPC(conn *net.UDPConn, dev *net.UDPAddr, object uuid16, handle func(rpcHeader, []byte) []byte) *pnioRPC {
	r := &pnioRPC{conn: conn, dev: dev, object: object, activity: newUUID(), ourBoot: uint32(time.Now().Unix()),
		replies: make(chan rpcIn, 8), handle: handle, closed: make(chan struct{})}
	go r.readLoop()
	return r
}

func (r *pnioRPC) readLoop() {
	buf := make([]byte, 2048)
	for {
		n, from, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			close(r.closed)
			return
		}
		h, body, err := decodeRPC(buf[:n])
		if err != nil {
			continue
		}
		switch h.PType {
		case rpcRequest:
			if r.handle == nil {
				continue
			}
			res := r.handle(h, body)
			if res == nil {
				continue
			}
			out := encodeRPC(rpcHeader{PType: rpcResponse, Flags1: rpcFlagLastFrag | rpcFlagNoFack, Object: h.Object,
				Interface: h.Interface, Activity: h.Activity, ServerBoot: r.ourBoot, IfVersion: h.IfVersion,
				Seq: h.Seq, Opnum: h.Opnum}, res)
			r.conn.WriteToUDP(out, from)
		case rpcPing:
			// The response got lost or is late: say we are working on it.
			out := encodeRPC(rpcHeader{PType: rpcWorking, Object: h.Object, Interface: h.Interface,
				Activity: h.Activity, ServerBoot: r.ourBoot, IfVersion: h.IfVersion, Seq: h.Seq, Opnum: h.Opnum}, nil)
			r.conn.WriteToUDP(out, from)
		default:
			if h.Activity != r.activity {
				continue
			}
			select {
			case r.replies <- rpcIn{h, append([]byte(nil), body...)}:
			default:
			}
		}
	}
}

// call sends one request and waits for its response (1 s, 4 tries).
func (r *pnioRPC) call(opnum uint16, blocks []byte) ([4]byte, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seq := r.seq
	r.seq++
	for len(r.replies) > 0 {
		<-r.replies
	}
	pkt := encodeRPC(rpcHeader{PType: rpcRequest, Flags1: rpcFlagIdempoten, Object: r.object, Interface: pnioDeviceIf,
		Activity: r.activity, ServerBoot: r.boot, IfVersion: 1, Seq: seq, Opnum: opnum}, ndrRequest(blocks))
	for try := 0; try < 4; try++ {
		if _, err := r.conn.WriteToUDP(pkt, r.dev); err != nil {
			return [4]byte{}, nil, err
		}
		timeout := time.NewTimer(time.Second)
	wait:
		for {
			select {
			case <-r.closed:
				timeout.Stop()
				return [4]byte{}, nil, errors.New("RPC socket closed")
			case <-timeout.C:
				break wait
			case in := <-r.replies:
				if in.h.Seq != seq {
					continue
				}
				switch in.h.PType {
				case rpcResponse:
					timeout.Stop()
					r.boot = in.h.ServerBoot
					st, blocks, err := parseNDRResponse(in.body, in.h.LittleEndian)
					return st, blocks, err
				case rpcFault:
					timeout.Stop()
					var code uint32
					if len(in.body) >= 4 {
						code = byteOrder(in.h.LittleEndian).Uint32(in.body)
					}
					return [4]byte{}, nil, fmt.Errorf("RPC fault 0x%08X", code)
				case rpcReject:
					timeout.Stop()
					return [4]byte{}, nil, errors.New("RPC request rejected by the device")
				case rpcWorking:
					timeout.Reset(2 * time.Second)
				case rpcNoCall:
					break wait // the device lost it: send again
				}
			}
		}
	}
	return [4]byte{}, nil, fmt.Errorf("no answer from %s (UDP %d)", r.dev.IP, r.dev.Port)
}

// ---- session ----

// pnioSession is one running IO test.
type pnioSession struct {
	cfg  pnioConfig
	link dcpLink
	conn *net.UDPConn
	rpc  *pnioRPC
	ar   pnioAR
	in   pnioCRLayout
	out  pnioCRLayout

	mu        sync.Mutex
	state     string
	outBuf    []byte // output C_SDU (data, IOPS, IOCS)
	armed     bool
	inBuf     []byte // last input C_SDU
	inStatus  byte
	inCount   uint64
	lastIn    time.Time
	sent      uint64
	late      uint64
	maxLate   time.Duration
	connected bool
	aborted   string
	rta       [2]rtaState

	events   chan string
	appReady chan struct{}
	readyOne sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// pnioSnapshot is what the UI shows.
type pnioSnapshot struct {
	State    string
	Armed    bool
	Inputs   []pnioValue
	Outputs  []pnioValue
	InStatus byte
	InCount  uint64
	Sent     uint64
	Late     uint64
	MaxLate  time.Duration
	LastIn   time.Time
}

type pnioValue struct {
	Slot, Subslot uint16
	Data          []byte
	IOPS, IOCS    byte // IOPS of the provider, IOCS of the consumer
}

func (s *pnioSession) event(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if s.cfg.OnEvent != nil {
		s.cfg.OnEvent(msg)
		return
	}
	select {
	case s.events <- msg:
	default:
	}
}

func (s *pnioSession) setState(st string) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
	s.event("state: %s", st)
}

// startPNIO connects to the device and runs the session until Close. It owns
// link and conn from now on (also on error).
func startPNIO(cfg pnioConfig, link dcpLink, conn *net.UDPConn) (*pnioSession, error) {
	if err := cfg.validate(); err != nil {
		link.Close()
		conn.Close()
		return nil, err
	}
	if cfg.DevicePort == 0 {
		cfg.DevicePort = pnioRPCPort
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 15 * time.Second
	}
	s := &pnioSession{cfg: cfg, link: link, conn: conn, state: "connecting",
		events: make(chan string, 256), appReady: make(chan struct{}), stop: make(chan struct{})}
	s.in, s.out = pnioLayout(cfg.Slots)
	s.ar = pnioAR{UUID: newUUID(), SessionKey: uint16(rand.UintN(0xFFFF) + 1),
		InFrameID: 0xC010, OutFrameID: 0xC011, LocalAlarmRef: 0x0001,
		ControllerObject: pnioObjectUUID(1, 0x0001, 0x0000)}
	s.rta[0].init()
	s.rta[1].init()
	s.outBuf = make([]byte, s.out.Length)
	s.applyIOPS()
	for _, c := range s.out.IOCS {
		s.outBuf[c.Off] = 0x80 // we consume the inputs: IOCS good
	}

	dev := &net.UDPAddr{IP: cfg.DeviceIP, Port: cfg.DevicePort}
	s.rpc = newPNIORPC(conn, dev, pnioObjectUUID(1, cfg.DeviceID, cfg.VendorID), s.serve)

	s.wg.Add(1)
	go s.recvLoop()

	fail := func(err error) (*pnioSession, error) {
		s.Close()
		return nil, err
	}
	st, res, err := s.rpc.call(opConnect, buildConnect(&cfg, &s.ar, link.MAC(), s.in, s.out))
	if err != nil {
		return fail(fmt.Errorf("connect: %w", err))
	}
	if !pnioStatusOK(st) {
		return fail(fmt.Errorf("connect refused: %s", pnioStatusText(st)))
	}
	diff, err := parseConnectRes(res, &s.ar)
	if err != nil {
		return fail(err)
	}
	s.mu.Lock()
	s.connected = true
	s.mu.Unlock()
	s.event("connected: AR %s, input frame 0x%04X, output frame 0x%04X, device alarm ref 0x%04X",
		s.ar.UUID, s.ar.InFrameID, s.ar.OutFrameID, s.ar.DevAlarm)
	if diff != "" {
		s.event("the device differs from the expected configuration:\n%s", diff)
	}

	s.wg.Add(1)
	go s.sendLoop()

	s.setState("parametrizing")
	st, _, err = s.rpc.call(opControl, appendBlock(nil, 0x0110, controlBody(&s.ar, ctlPrmEnd)))
	if err != nil {
		return fail(fmt.Errorf("PrmEnd: %w", err))
	}
	if !pnioStatusOK(st) {
		return fail(fmt.Errorf("PrmEnd refused: %s", pnioStatusText(st)))
	}

	s.setState("waiting for ApplicationReady")
	select {
	case <-s.appReady:
	case <-time.After(cfg.ConnectTimeout):
		return fail(errors.New("the device did not send ApplicationReady. It sends it to UDP port 34964 on this PC: " +
			"check the firewall, and that no other program (PRONETA, TIA Portal) uses that port"))
	}
	s.setState("data exchange")
	return s, nil
}

// serve answers requests from the device: ApplicationReady (IOXControlReq).
func (s *pnioSession) serve(h rpcHeader, body []byte) []byte {
	maxCount, blocks, err := parseNDRRequest(body, h.LittleEndian)
	if err != nil || h.Opnum != opControl {
		return ndrResponse([4]byte{0xDD, 0x81, 0x40, 0x01}, maxCount, nil)
	}
	list, _ := splitBlocks(blocks)
	for _, bl := range list {
		if bl.Type != 0x0112 {
			continue
		}
		r := &beReader{b: bl.Body}
		r.u16()
		id, key := r.uuid(), r.u16()
		r.u16()
		cmd := r.u16()
		if r.bad || id != s.ar.UUID {
			return ndrResponse([4]byte{0xDD, 0x81, 0x40, 0x05}, maxCount, nil)
		}
		if cmd&ctlAppReady != 0 {
			s.readyOne.Do(func() {
				s.event("device: ApplicationReady")
				close(s.appReady)
			})
		}
		ar := s.ar
		ar.SessionKey = key
		return ndrResponse([4]byte{}, maxCount, appendBlock(nil, 0x8112, controlBody(&ar, ctlDone)))
	}
	return ndrResponse([4]byte{0xDD, 0x81, 0x15, 0x00}, maxCount, nil)
}

// Close releases the AR (if any) and stops everything.
func (s *pnioSession) Close() {
	s.mu.Lock()
	connected := s.connected && s.aborted == ""
	s.connected = false
	s.mu.Unlock()
	if connected {
		st, _, err := s.rpc.call(opRelease, appendBlock(nil, 0x0114, controlBody(&s.ar, ctlRelease)))
		switch {
		case err != nil:
			s.event("release: %v (the device drops the AR when its watchdog expires)", err)
		case !pnioStatusOK(st):
			s.event("release: %s", pnioStatusText(st))
		default:
			s.event("released")
		}
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.conn.Close()
	s.wg.Wait()
	s.link.Close()
}

// Events delivers state changes, alarms and errors.
func (s *pnioSession) Events() <-chan string { return s.events }

// Arm switches the outputs to IOPS GOOD (they act) or back to BAD (the device
// uses its substitute values). Disarming also clears the output data.
func (s *pnioSession) Arm(on bool) {
	s.mu.Lock()
	s.armed = on
	if !on {
		for _, d := range s.out.Data {
			clear(s.outBuf[d.Off : d.Off+d.Len])
		}
	}
	s.applyIOPS()
	s.mu.Unlock()
	s.event("outputs %s", map[bool]string{true: "ARMED (IOPS good)", false: "disarmed (IOPS bad, substitute values)"}[on])
}

func (s *pnioSession) applyIOPS() {
	v := byte(0x00)
	if s.armed {
		v = 0x80
	}
	for _, d := range s.out.Data {
		s.outBuf[d.Off+d.Len] = v
	}
}

// SetOutput writes the output data of one submodule. It needs Arm first.
func (s *pnioSession) SetOutput(slot, subslot uint16, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.armed {
		return errors.New("outputs are not armed")
	}
	for _, d := range s.out.Data {
		if d.Slot == slot && d.Subslot == subslot {
			if len(data) != d.Len {
				return fmt.Errorf("slot %d.%d has %d output bytes", slot, subslot, d.Len)
			}
			copy(s.outBuf[d.Off:], data)
			return nil
		}
	}
	return fmt.Errorf("slot %d.%d has no outputs", slot, subslot)
}

func (s *pnioSession) Snapshot() pnioSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := pnioSnapshot{State: s.state, Armed: s.armed, InStatus: s.inStatus, InCount: s.inCount,
		Sent: s.sent, Late: s.late, MaxLate: s.maxLate, LastIn: s.lastIn}
	iocs := func(l pnioCRLayout, buf []byte, slot, sub uint16) byte {
		for _, c := range l.IOCS {
			if c.Slot == slot && c.Subslot == sub && c.Off < len(buf) {
				return buf[c.Off]
			}
		}
		return 0
	}
	for _, d := range s.in.Data {
		if d.Len == 0 {
			continue
		}
		v := pnioValue{Slot: d.Slot, Subslot: d.Subslot, Data: make([]byte, d.Len)}
		if s.inBuf != nil {
			copy(v.Data, s.inBuf[d.Off:])
			v.IOPS = s.inBuf[d.Off+d.Len]
		}
		v.IOCS = iocs(s.out, s.outBuf, d.Slot, d.Subslot)
		sn.Inputs = append(sn.Inputs, v)
	}
	for _, d := range s.out.Data {
		v := pnioValue{Slot: d.Slot, Subslot: d.Subslot, Data: append([]byte(nil), s.outBuf[d.Off:d.Off+d.Len]...),
			IOPS: s.outBuf[d.Off+d.Len]}
		if s.inBuf != nil {
			v.IOCS = iocs(s.in, s.inBuf, d.Slot, d.Subslot)
		}
		sn.Outputs = append(sn.Outputs, v)
	}
	return sn
}

// dataStatusText explains the APDU DataStatus byte.
func dataStatusText(b byte) string {
	var p []string
	if b&0x01 == 0 {
		p = append(p, "backup")
	}
	if b&0x04 == 0 {
		p = append(p, "data invalid")
	}
	if b&0x10 == 0 {
		p = append(p, "provider STOP")
	} else {
		p = append(p, "RUN")
	}
	if b&0x20 == 0 {
		p = append(p, "station problem")
	}
	return fmt.Sprintf("0x%02X %s", b, strings.Join(p, ", "))
}
