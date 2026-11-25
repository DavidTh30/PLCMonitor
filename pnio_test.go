package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPNIOLayoutPnetSample(t *testing.T) {
	in, out := pnioLayout(pnetSampleSlots(1))
	wantIn := []pnioPos{{0, 1, 0, 0}, {0, 0x8000, 1, 0}, {0, 0x8001, 2, 0}, {1, 1, 3, 1}}
	if len(in.Data) != len(wantIn) {
		t.Fatalf("input data %+v", in.Data)
	}
	for i, w := range wantIn {
		if in.Data[i] != w {
			t.Errorf("input %d = %+v, want %+v", i, in.Data[i], w)
		}
	}
	if len(in.IOCS) != 1 || in.IOCS[0] != (pnioIOCSPos{2, 1, 5}) || in.Length != 40 {
		t.Errorf("input IOCS %+v length %d", in.IOCS, in.Length)
	}
	if len(out.Data) != 1 || out.Data[0] != (pnioPos{2, 1, 0, 1}) {
		t.Errorf("output data %+v", out.Data)
	}
	wantIOCS := []pnioIOCSPos{{0, 1, 2}, {0, 0x8000, 3}, {0, 0x8001, 4}, {1, 1, 5}}
	for i, w := range wantIOCS {
		if i >= len(out.IOCS) || out.IOCS[i] != w {
			t.Errorf("output IOCS %+v, want %+v", out.IOCS, wantIOCS)
			break
		}
	}
}

func TestPNIOBlockLengths(t *testing.T) {
	cfg := pnioConfig{SendClockFactor: 32, ReductionRatio: 32, WatchdogFactor: 10, Slots: pnetSampleSlots(1)}
	ar := pnioAR{UUID: newUUID(), SessionKey: 1, InFrameID: 0xC010, OutFrameID: 0xC011, LocalAlarmRef: 1}
	in, out := pnioLayout(cfg.Slots)
	list, err := splitBlocks(buildConnect(&cfg, &ar, net.HardwareAddr{2, 0, 0, 0, 0, 1}, in, out))
	if err != nil {
		t.Fatal(err)
	}
	types := []uint16{0x0101, 0x0102, 0x0102, 0x0103, 0x0104, 0x0104, 0x0104}
	if len(list) != len(types) {
		t.Fatalf("%d blocks", len(list))
	}
	for i, b := range list {
		if b.Type != types[i] {
			t.Errorf("block %d type 0x%04x", i, b.Type)
		}
	}
	if n := len(list[0].Body) + 2; n != 54+len(pnioStationName) {
		t.Errorf("ARBlockReq length %d", n)
	}
	if n := len(list[3].Body) + 2; n != 0x16 {
		t.Errorf("AlarmCRBlockReq length 0x%x, want 0x16", n)
	}
	if n := len(controlBody(&ar, ctlPrmEnd)) + 2; n != 0x1C {
		t.Errorf("IODControlReq length 0x%x, want 0x1C", n)
	}
	// Input IOCR: 40 fixed bytes + API 4 + count 2 + 4Ã—6 + count 2 + 1Ã—6
	if n := len(list[1].Body); n != 40+4+2+24+2+6 {
		t.Errorf("input IOCR body %d", n)
	}
	h := rpcHeader{PType: rpcRequest, Object: pnioObjectUUID(1, 2, 0x493), Interface: pnioDeviceIf, Activity: newUUID(), Seq: 7, Opnum: 4}
	p := encodeRPC(h, []byte{1, 2, 3})
	if len(p) != 83 {
		t.Fatalf("RPC datagram %d bytes", len(p))
	}
	g, body, err := decodeRPC(p)
	if err != nil || g.Object != h.Object || g.Interface != h.Interface || g.Seq != 7 || g.Opnum != 4 || !bytes.Equal(body, []byte{1, 2, 3}) {
		t.Fatalf("RPC round trip: %+v %v %v", g, body, err)
	}
	if !strings.HasPrefix(h.Object.String(), "dea00000-6c97-11d1-8271-00010002") {
		t.Errorf("object UUID %s", h.Object)
	}
	// UUID in little-endian representation: first field reversed.
	if p[8] != 0x00 || p[11] != 0xDE {
		t.Errorf("object UUID bytes % x", p[8:24])
	}
}

func TestParsePNIOSlots(t *testing.T) {
	s, err := parsePNIOSlots("1:0x30/0x130:i1, 3:0x32/0x132:i1o1")
	if err != nil || len(s) != 2 || s[1].Slot != 3 || s[1].Module != 0x32 || s[1].Subs[0].In != 1 || s[1].Subs[0].Out != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := parsePNIOSlots("1:0x30:i1"); err == nil {
		t.Error("missing submodule accepted")
	}
}

// ---- fake device ----

// chanLink is one end of a fake Ethernet cable.
type chanLink struct {
	mac  net.HardwareAddr
	in   chan []byte
	peer *chanLink
}

func cablePair(a, b net.HardwareAddr) (*chanLink, *chanLink) {
	x := &chanLink{mac: a, in: make(chan []byte, 512)}
	y := &chanLink{mac: b, in: make(chan []byte, 512), peer: x}
	x.peer = y
	return x, y
}

func (c *chanLink) MAC() net.HardwareAddr { return c.mac }
func (c *chanLink) Close() error          { return nil }
func (c *chanLink) Send(f []byte) error {
	select {
	case c.peer.in <- append([]byte(nil), f...):
	default:
	}
	return nil
}
func (c *chanLink) Recv(deadline time.Time) ([]byte, error) {
	select {
	case f := <-c.in:
		return f, nil
	case <-time.After(time.Until(deadline)):
		return nil, nil
	}
}

type fakePNDevice struct {
	t        *testing.T
	link     *chanLink
	conn     *net.UDPConn
	mu       sync.Mutex
	arUUID   uuid16
	session  uint16
	inFID    uint16
	outFID   uint16
	ctrlRef  uint16
	lastOut  []byte
	rtaRecv  [][]byte
	released bool
	prmEnd   bool
	slot1In  int // offset of slot 1's input byte
	slot2Out int // offset of slot 2's output byte
}

func (d *fakePNDevice) serve(ctrl *net.UDPAddr) {
	buf := make([]byte, 2048)
	for {
		n, from, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		h, body, err := decodeRPC(buf[:n])
		if err != nil {
			d.t.Errorf("device: %v", err)
			continue
		}
		if h.PType == rpcResponse { // to our ApplicationReady
			st, blocks, _ := parseNDRResponse(body, h.LittleEndian)
			list, _ := splitBlocks(blocks)
			if !pnioStatusOK(st) || len(list) != 1 || list[0].Type != 0x8112 {
				d.t.Errorf("ApplicationReady response: %v %+v", st, list)
			}
			continue
		}
		if h.Interface != pnioDeviceIf {
			d.t.Errorf("device: interface %s", h.Interface)
		}
		maxCount, blocks, err := parseNDRRequest(body, h.LittleEndian)
		if err != nil {
			d.t.Errorf("device: %v", err)
			continue
		}
		list, err := splitBlocks(blocks)
		if err != nil {
			d.t.Errorf("device: %v", err)
			continue
		}
		var res []byte
		switch h.Opnum {
		case opConnect:
			for _, bl := range list {
				r := &beReader{b: bl.Body}
				switch bl.Type {
				case 0x0101:
					r.u16()
					d.mu.Lock()
					d.arUUID, d.session = r.uuid(), r.u16()
					d.mu.Unlock()
				case 0x0102:
					typ, _, _, _ := r.u16(), r.u16(), r.u16(), r.u32()
					r.u16() // data length
					fid := r.u16()
					r.take(2 + 2 + 2 + 2 + 4 + 2 + 2 + 2 + 6 + 2 + 4)
					for i := r.u16(); i > 0; i-- {
						slot, _, off := r.u16(), r.u16(), r.u16()
						if typ == 1 && slot == 1 {
							d.slot1In = int(off)
						}
						if typ == 2 && slot == 2 {
							d.slot2Out = int(off)
						}
					}
					if typ == 1 {
						d.inFID = fid
					} else {
						d.outFID = fid
					}
				case 0x0103:
					r.take(2 + 2 + 4 + 2 + 2)
					d.ctrlRef = r.u16()
				}
			}
			arRes := be16(nil, 1)
			arRes = append(arRes, d.arUUID[:]...)
			arRes = be16(arRes, d.session)
			arRes = append(arRes, d.link.mac...)
			arRes = be16(arRes, 0x8892)
			res = appendBlock(res, 0x8101, arRes)
			res = appendBlock(res, 0x8102, be16(be16(be16(nil, 1), 1), d.inFID))
			res = appendBlock(res, 0x8102, be16(be16(be16(nil, 2), 2), d.outFID))
			res = appendBlock(res, 0x8103, be16(be16(be16(nil, 1), 0x0003), 200))
			go d.cyclic()
		case opControl:
			d.mu.Lock()
			d.prmEnd = true
			ar := pnioAR{UUID: d.arUUID, SessionKey: d.session}
			d.mu.Unlock()
			res = appendBlock(nil, 0x8110, controlBody(&ar, ctlDone))
			go func() { // ApplicationReady, as RPC client to the controller
				time.Sleep(20 * time.Millisecond)
				req := encodeRPC(rpcHeader{PType: rpcRequest, Flags1: rpcFlagIdempoten, Object: newUUID(),
					Interface: pnioControllerIf, Activity: newUUID(), IfVersion: 1, Opnum: opControl},
					ndrRequest(appendBlock(nil, 0x0112, controlBody(&ar, ctlAppReady))))
				d.conn.WriteToUDP(req, ctrl)
			}()
		case opRelease:
			d.mu.Lock()
			d.released = true
			ar := pnioAR{UUID: d.arUUID, SessionKey: d.session}
			d.mu.Unlock()
			res = appendBlock(nil, 0x8114, controlBody(&ar, ctlDone))
		}
		out := encodeRPC(rpcHeader{PType: rpcResponse, Object: h.Object, Interface: h.Interface, Activity: h.Activity,
			ServerBoot: 42, IfVersion: 1, Seq: h.Seq, Opnum: h.Opnum}, ndrResponse([4]byte{}, maxCount, res))
		d.conn.WriteToUDP(out, from)
	}
}

// cyclic sends input frames (slot 1 = 0xA5) and records output frames and
// alarm frames from the controller.
func (d *fakePNDevice) cyclic() {
	frame := append(append([]byte{}, d.link.peer.mac...), d.link.mac...)
	frame = be16(frame, 0x8892)
	frame = be16(frame, d.inFID)
	cs := make([]byte, 40)
	for i := range cs[:6] {
		cs[i] = 0x80 // IOPS of DAP subs, slot 1 data/IOPS, IOCS of slot 2
	}
	cs[d.slot1In] = 0xA5
	frame = append(frame, cs...)
	frame = append(frame, 0, 0, 0x35, 0)
	for i := 0; i < 400; i++ {
		d.link.Send(frame)
		deadline := time.Now().Add(5 * time.Millisecond)
		for {
			f, _ := d.link.Recv(deadline)
			if f == nil {
				break
			}
			_, p, _ := etherPayload(f, 0x8892)
			fid := binary.BigEndian.Uint16(p)
			d.mu.Lock()
			if fid == d.outFID {
				d.lastOut = append([]byte(nil), p[2:42]...)
			} else if fid == rtaFrameLow || fid == rtaFrameHigh {
				d.rtaRecv = append(d.rtaRecv, append([]byte(nil), p...))
			}
			d.mu.Unlock()
		}
	}
}

func (d *fakePNDevice) out() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastOut
}

func pnioWait(t *testing.T, what string, f func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestPNIOSessionWithFakeDevice(t *testing.T) {
	pcMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}
	devMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x02}
	ctrlLink, devLink := cablePair(pcMAC, devMAC)
	lo := net.IPv4(127, 0, 0, 1)
	devConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	defer devConn.Close()
	ctrlConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	dev := &fakePNDevice{t: t, link: devLink, conn: devConn}
	go dev.serve(ctrlConn.LocalAddr().(*net.UDPAddr))

	var evMu sync.Mutex
	var events []string
	cfg := pnioConfig{DeviceIP: lo, DevicePort: devConn.LocalAddr().(*net.UDPAddr).Port, DeviceMAC: devMAC,
		VendorID: 0x0493, DeviceID: 0x0002, Slots: pnetSampleSlots(1),
		SendClockFactor: 32, ReductionRatio: 4, WatchdogFactor: 10, ConnectTimeout: 2 * time.Second,
		OnEvent: func(e string) { evMu.Lock(); events = append(events, e); evMu.Unlock() }}
	s, err := startPNIO(cfg, peerLink{ctrlLink, devMAC}, ctrlConn)
	if err != nil {
		t.Fatal(err)
	}
	if st := s.Snapshot().State; st != "data exchange" {
		t.Fatalf("state %q", st)
	}

	pnioWait(t, "input 0xA5", func() bool {
		in := s.Snapshot().Inputs
		return len(in) == 1 && in[0].Slot == 1 && in[0].Data[0] == 0xA5 && in[0].IOPS == 0x80
	})
	pnioWait(t, "output frames", func() bool { return dev.out() != nil })
	if o := dev.out(); o[dev.slot2Out+1] != 0x00 {
		t.Fatalf("outputs must start with IOPS bad, got % x", o[:8])
	}
	if err := s.SetOutput(2, 1, []byte{0x3C}); err == nil {
		t.Fatal("SetOutput worked before Arm")
	}
	s.Arm(true)
	if err := s.SetOutput(2, 1, []byte{0x3C}); err != nil {
		t.Fatal(err)
	}
	pnioWait(t, "output 0x3C with IOPS good", func() bool {
		o := dev.out()
		return o[dev.slot2Out] == 0x3C && o[dev.slot2Out+1] == 0x80
	})
	if o := dev.out(); o[2] != 0x80 || o[5] != 0x80 {
		t.Errorf("IOCS for the inputs not good: % x", o[:8])
	}

	// A diagnosis alarm (low priority, first DATA has sequence number 0).
	note := be16(nil, 1) // diagnosis
	note = be32(note, 0)
	note = be16(note, 1)
	note = be16(note, 1)
	note = be32(note, 0x30)
	note = be32(note, 0x130)
	note = be16(note, 0x0801)
	sdu := appendBlock(nil, 0x0002, note)
	f := append(append([]byte{}, pcMAC...), devMAC...)
	f = be16(f, 0x8892)
	f = be16(f, rtaFrameLow)
	f = be16(f, 0x0001) // to the controller's alarm reference
	f = be16(f, 0x0003)
	f = append(f, 0x11, 0x11)
	f = be16(f, 0)      // SendSeqNum
	f = be16(f, 0xFFFE) // AckSeqNum
	f = be16(f, uint16(len(sdu)))
	devLink.Send(append(f, sdu...))
	pnioWait(t, "alarm ACK and AlarmAck", func() bool {
		dev.mu.Lock()
		defer dev.mu.Unlock()
		return len(dev.rtaRecv) >= 2
	})
	dev.mu.Lock()
	ack, data := dev.rtaRecv[0], dev.rtaRecv[1]
	dev.mu.Unlock()
	if ack[6] != 0x13 || binary.BigEndian.Uint16(ack[10:]) != 0 || binary.BigEndian.Uint16(ack[2:]) != 0x0003 {
		t.Errorf("ACK frame % x", ack[:14])
	}
	if data[6] != 0x11 || binary.BigEndian.Uint16(data[8:]) != 0 {
		t.Errorf("DATA frame % x", data[:14])
	}
	if l, _ := splitBlocks(data[14 : 14+int(binary.BigEndian.Uint16(data[12:]))]); len(l) != 1 || l[0].Type != 0x8002 {
		t.Errorf("AlarmAck block %+v", l)
	}

	s.Close()
	dev.mu.Lock()
	if !dev.prmEnd || !dev.released {
		t.Errorf("prmEnd %v released %v", dev.prmEnd, dev.released)
	}
	dev.mu.Unlock()
	evMu.Lock()
	all := strings.Join(events, "\n")
	evMu.Unlock()
	for _, w := range []string{"ApplicationReady", "diagnosis alarm", "released"} {
		if !strings.Contains(all, w) {
			t.Errorf("no %q event in:\n%s", w, all)
		}
	}
}

// fakePNIOSession connects a session to a fake device (see TestPNIOSessionWithFakeDevice).
func fakePNIOSession(t *testing.T, onEvent func(string)) (*pnioSession, *fakePNDevice) {
	t.Helper()
	pcMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x11}
	devMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x12}
	ctrlLink, devLink := cablePair(pcMAC, devMAC)
	lo := net.IPv4(127, 0, 0, 1)
	devConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { devConn.Close() })
	ctrlConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lo})
	if err != nil {
		t.Fatal(err)
	}
	dev := &fakePNDevice{t: t, link: devLink, conn: devConn}
	go dev.serve(ctrlConn.LocalAddr().(*net.UDPAddr))
	cfg := pnioConfig{DeviceIP: lo, DevicePort: devConn.LocalAddr().(*net.UDPAddr).Port, DeviceMAC: devMAC,
		Slots: pnetSampleSlots(1), SendClockFactor: 32, ReductionRatio: 4, WatchdogFactor: 10,
		ConnectTimeout: 2 * time.Second, OnEvent: onEvent}
	s, err := startPNIO(cfg, peerLink{ctrlLink, devMAC}, ctrlConn)
	if err != nil {
		t.Fatal(err)
	}
	return s, dev
}

func TestPNIOAPI(t *testing.T) {
	m := &pnioManager{}
	old := pnioMgr
	pnioMgr = m
	defer func() { pnioMgr = old }()
	s, dev := fakePNIOSession(t, func(e string) { m.event("%s", e) })
	m.sess, m.device = s, pnioStartReq{IP: "127.0.0.1"}

	mux := http.NewServeMux()
	jsonOnly := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return false
		}
		return true
	}
	pnioRoutes(mux, false, jsonOnly)
	call := func(method, path, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:5000"
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var j map[string]any
		json.Unmarshal(rec.Body.Bytes(), &j)
		return rec.Code, j
	}

	pnioWait(t, "input 0xA5 in the status", func() bool {
		_, j := call("GET", "/api/pnio/status", "")
		in, _ := j["inputs"].([]any)
		if len(in) != 1 {
			return false
		}
		d := in[0].(map[string]any)["data"].([]any)
		return j["state"] == "data exchange" && d[0].(float64) == 0xA5
	})
	if code, _ := call("POST", "/api/pnio/output", `{"slot":2,"hex":"0f"}`); code != http.StatusBadRequest {
		t.Errorf("output before arm: HTTP %d", code)
	}
	if code, _ := call("POST", "/api/pnio/arm", `{"on":true}`); code != http.StatusOK {
		t.Fatalf("arm: HTTP %d", code)
	}
	if code, j := call("POST", "/api/pnio/output", `{"slot":2,"hex":"0f"}`); code != http.StatusOK {
		t.Fatalf("output: HTTP %d %v", code, j)
	}
	pnioWait(t, "output 0x0F at the device", func() bool {
		o := dev.out()
		return o != nil && o[dev.slot2Out] == 0x0F && o[dev.slot2Out+1] == 0x80
	})

	// Dead-man: no poll for more than 3 s disarms the outputs.
	m.mu.Lock()
	m.lastPoll = time.Now().Add(-4 * time.Second)
	m.mu.Unlock()
	m.check()
	if s.Snapshot().Armed {
		t.Fatal("still armed after 4 s without a poll")
	}
	pnioWait(t, "outputs back to IOPS bad", func() bool { o := dev.out(); return o[dev.slot2Out] == 0 && o[dev.slot2Out+1] == 0 })

	if code, _ := call("POST", "/api/pnio/stop", `{}`); code != http.StatusOK {
		t.Fatalf("stop: HTTP %d", code)
	}
	pnioWait(t, "release", func() bool {
		dev.mu.Lock()
		defer dev.mu.Unlock()
		return dev.released
	})
	if _, j := call("GET", "/api/pnio/status", ""); j["running"] != false {
		t.Errorf("still running after stop: %v", j)
	}
	// From another computer: refused.
	req := httptest.NewRequest("GET", "/api/pnio/status", nil)
	req.RemoteAddr = "192.168.0.9:5000"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("remote status: HTTP %d", rec.Code)
	}
}
