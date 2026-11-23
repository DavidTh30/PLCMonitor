package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var (
	pcMAC  = net.HardwareAddr{0xd4, 0x93, 0x90, 0x73, 0xa9, 0x52}
	plcMAC = net.HardwareAddr{0x28, 0x63, 0x36, 0x01, 0x02, 0x03}
)

func TestDCPIdentifyFrame(t *testing.T) {
	f := buildIdentify(pcMAC, 0x01020304)
	want := "010ecf000000" + "d4939073a952" + "8892" + // Ethernet
		"fefe" + "0500" + "01020304" + "0080" + "0004" + "ffff0000" // DCP Identify All
	if got := hex.EncodeToString(f[:len(want)/2]); got != want {
		t.Errorf("identify frame\n got %s\nwant %s", got, want)
	}
	if len(f) != 60 {
		t.Errorf("not padded to 60 bytes: %d", len(f))
	}
}

func TestDCPSetFrame(t *testing.T) {
	ip, mask, gw, err := checkIPSettings("192.168.0.10", "255.255.255.0", "192.168.0.1")
	if err != nil {
		t.Fatal(err)
	}
	f := buildSet(plcMAC, pcMAC, 7, []dcpBlock{ipBlock(ip, mask, gw, true), nameBlock("plc-1", false)})
	want := "286336010203" + "d4939073a952" + "8892" +
		"fefd" + "0400" + "00000007" + "0000" + "001e" + // header, data length 30 (18 + 11 + 1 pad)
		"0102" + "000e" + "0001" + "c0a8000a" + "ffffff00" + "c0a80001" + // IP block, permanent
		"0202" + "0007" + "0000" + "706c632d31" + "00" // name block, temporary, padded to even
	if got := hex.EncodeToString(f[:len(want)/2]); got != want {
		t.Errorf("set frame\n got %s\nwant %s", got, want)
	}
	if s := signalBlock(); s.Opt != 5 || s.Sub != 3 || !bytes.Equal(s.Data, []byte{1, 0}) {
		t.Errorf("signal block %+v", s)
	}
}

// identifyResponse builds what an S7-1200 answers, optionally VLAN tagged.
func identifyResponse(xid uint32, vlan bool) []byte {
	var blocks []byte
	add := func(opt, sub byte, info uint16, data []byte) {
		blocks = append(blocks, opt, sub)
		blocks = binary.BigEndian.AppendUint16(blocks, uint16(2+len(data)))
		blocks = binary.BigEndian.AppendUint16(blocks, info)
		blocks = append(blocks, data...)
		if len(data)%2 == 1 {
			blocks = append(blocks, 0)
		}
	}
	add(0x02, 0x01, 0, []byte("S7-1200"))
	add(0x02, 0x02, 0, []byte("plc-line1"))
	add(0x02, 0x03, 0, []byte{0x00, 0x2a, 0x01, 0x0d})
	add(0x02, 0x04, 0, []byte{0x03, 0x00}) // IO device + IO controller
	add(0x01, 0x02, 0x0001, []byte{192, 168, 0, 1, 255, 255, 255, 0, 0, 0, 0, 0})
	p := append([]byte(nil), pcMAC...)
	p = append(p, plcMAC...)
	if vlan {
		p = append(p, 0x81, 0x00, 0xc0, 0x00)
	}
	p = append(p, 0x88, 0x92, 0xfe, 0xff, 0x05, 0x01)
	p = binary.BigEndian.AppendUint32(p, xid)
	p = append(p, 0, 0)
	p = binary.BigEndian.AppendUint16(p, uint16(len(blocks)))
	return append(p, blocks...)
}

func TestDCPParseIdentify(t *testing.T) {
	for _, vlan := range []bool{false, true} {
		d, ok := parseIdentify(identifyResponse(42, vlan), 42)
		if !ok || d.MAC != plcMAC.String() || d.Name != "plc-line1" || d.TypeOfStation != "S7-1200" ||
			d.Vendor != "Vendor 002A" || d.DeviceID != 0x010d || d.IP != "192.168.0.1" || d.Mask != "255.255.255.0" ||
			!d.IPSet || d.Role != "IO device, IO controller" {
			t.Errorf("vlan=%v: %+v ok=%v", vlan, d, ok)
		}
	}
	if _, ok := parseIdentify(identifyResponse(42, false), 43); ok {
		t.Error("answer to another request accepted")
	}
	if _, ok := parseIdentify([]byte{1, 2, 3}, 0); ok {
		t.Error("short frame accepted")
	}
}

func setResponse(xid uint32, errCode byte) []byte {
	p := append([]byte(nil), pcMAC...)
	p = append(p, plcMAC...)
	p = append(p, 0x88, 0x92, 0xfe, 0xfd, 0x04, 0x01)
	p = binary.BigEndian.AppendUint32(p, xid)
	p = append(p, 0, 0, 0, 8)
	p = append(p, 0x05, 0x04, 0x00, 0x03, 0x01, 0x02, errCode, 0x00) // result for IP (1/2)
	return p
}

func TestDCPParseSetResponse(t *testing.T) {
	if done, err := parseSetResponse(setResponse(9, 0), plcMAC, 9); !done || err != nil {
		t.Errorf("ok response: %v %v", done, err)
	}
	if done, err := parseSetResponse(setResponse(9, 5), plcMAC, 9); !done || err == nil || !strings.Contains(err.Error(), "IP address: set not possible") {
		t.Errorf("refused: %v %v", done, err)
	}
	if done, _ := parseSetResponse(setResponse(9, 0), pcMAC, 9); done {
		t.Error("answer from another device accepted")
	}
}

func TestDCPChecks(t *testing.T) {
	for _, ok := range []string{"plc-1", "plc-line1.cell-2", "a", "et200sp-3"} {
		if err := checkStationName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "PLC1", "plc_1", "-plc", "plc-", "port-001", "192.168.0.1", "a..b", strings.Repeat("a", 64)} {
		if err := checkStationName(bad); err == nil {
			t.Errorf("accepted name %q", bad)
		}
	}
	if _, _, _, err := checkIPSettings("192.168.0.10", "255.255.255.0", ""); err != nil {
		t.Errorf("no gateway: %v", err)
	}
	for _, c := range [][3]string{
		{"192.168.0.0", "255.255.255.0", ""},          // network address
		{"192.168.0.255", "255.255.255.0", ""},        // broadcast
		{"192.168.0.10", "255.0.255.0", ""},           // not a mask
		{"192.168.0.10", "255.255.255.0", "10.0.0.1"}, // gateway elsewhere
		{"300.1.1.1", "255.255.255.0", ""},
		{"127.0.0.5", "255.0.0.0", ""},
	} {
		if _, _, _, err := checkIPSettings(c[0], c[1], c[2]); err == nil {
			t.Errorf("accepted %v", c)
		}
	}
}

// fakeLink plays a PROFINET segment with one device.
type fakeLink struct {
	in      chan []byte
	sent    [][]byte
	refuse  bool
	devName string
}

func (f *fakeLink) MAC() net.HardwareAddr { return pcMAC }
func (f *fakeLink) Close() error          { return nil }
func (f *fakeLink) Recv(deadline time.Time) ([]byte, error) {
	select {
	case p := <-f.in:
		return p, nil
	case <-time.After(time.Until(deadline)):
		return nil, nil
	}
}
func (f *fakeLink) Send(frame []byte) error {
	f.sent = append(f.sent, frame)
	_, p, _ := dcpPayload(frame)
	xid := binary.BigEndian.Uint32(p[4:8])
	f.in <- []byte("noise that is not DCP at all")
	switch binary.BigEndian.Uint16(p[0:2]) {
	case 0xFEFE:
		f.in <- identifyResponse(xid, false)
		f.in <- identifyResponse(xid, false) // duplicate answers are merged
	case 0xFEFD:
		code := byte(0)
		if f.refuse {
			code = 5
		}
		f.in <- setResponse(xid, code)
	}
	return nil
}

func TestDCPAPI(t *testing.T) {
	link := &fakeLink{in: make(chan []byte, 8)}
	oldOpen := dcpOpen
	dcpOpen = func(string) (dcpLink, error) { return link, nil }
	defer func() { dcpOpen = oldOpen }()
	mon, _ := newTestMonitor(t, nil)
	mon.Update(func(c *Config) error { c.DCP.Source = "local"; return nil }) // this PC does DCP itself
	do := func(h http.Handler, path, body, remote string) (int, string) {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	h := NewWebHandler(mon, false)
	const local, remote = "127.0.0.1:1", "10.0.0.9:1"
	set := `{"adapter":"eth","mac":"28:63:36:01:02:03","set_ip":true,"ip":"192.168.0.20","mask":"255.255.255.0","set_name":true,"name":"plc-2","permanent":true}`
	if c, _ := do(h, "/api/dcp/set", set, remote); c != http.StatusForbidden {
		t.Errorf("remote set: %d", c)
	}
	if c, body := do(h, "/api/dcp/set", set, local); c != 200 {
		t.Errorf("set: %d %s", c, body)
	}
	last := link.sent[len(link.sent)-1]
	if !bytes.Contains(last, []byte("plc-2")) || !bytes.Contains(last, []byte{192, 168, 0, 20}) {
		t.Errorf("set frame: % x", last)
	}
	for _, bad := range []string{
		`{"mac":"28:63:36:01:02:03","set_name":true,"name":"PLC 2"}`,
		`{"mac":"28:63:36:01:02:03","set_ip":true,"ip":"192.168.0.255","mask":"255.255.255.0"}`,
		`{"mac":"nope","set_name":true,"name":"plc"}`,
		`{"mac":"28:63:36:01:02:03"}`,
	} {
		if c, _ := do(h, "/api/dcp/set", bad, local); c != 400 {
			t.Errorf("accepted %s: %d", bad, c)
		}
	}
	if c, body := do(h, "/api/dcp/identify", `{"adapter":"eth","wait_ms":1000}`, local); c != 200 || !strings.Contains(body, `"name":"plc-line1"`) {
		t.Errorf("identify: %d %s", c, body)
	}
	if c, _ := do(h, "/api/dcp/identify", `{}`, remote); c != http.StatusForbidden {
		t.Errorf("remote identify: %d", c)
	}
	if c, _ := do(NewWebHandler(mon, true), "/api/dcp/set", set, local); c != http.StatusForbidden {
		t.Errorf("set on a read-only dashboard: %d", c)
	}
}

// TestDCPAgent: the dashboard (on a PC without raw Ethernet) passes PROFINET
// requests to a Linux agent, which talks to the device.
func TestDCPAgent(t *testing.T) {
	link := &fakeLink{in: make(chan []byte, 8)}
	oldOpen, oldList := dcpOpen, dcpListAdapter
	dcpOpen = func(string) (dcpLink, error) { return link, nil }
	dcpListAdapter = func() ([]DCPAdapter, error) {
		return []DCPAdapter{{ID: "eth0", Name: "eth0", MAC: pcMAC.String(), IPs: []string{"192.168.0.50"}}}, nil
	}
	defer func() { dcpOpen, dcpListAdapter = oldOpen, oldList }()

	const token = "s3cret"
	agent := NewAgentHandler(token)
	var gotAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		r.RemoteAddr = "192.168.0.10:5000" // as if from the Windows PC
		agent.ServeHTTP(w, r)
	}))
	defer srv.Close()

	// The agent itself: no or wrong token → 401, also from its own computer.
	for _, auth := range []string{"", "Bearer nope"} {
		req := httptest.NewRequest("GET", "/api/dcp/adapters", nil)
		req.RemoteAddr = "127.0.0.1:1"
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		agent.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("agent with auth %q: %d", auth, rec.Code)
		}
	}

	mon, _ := newTestMonitor(t, nil)
	h := NewWebHandler(mon, false)
	do := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if mon.Config().DCP.Source != defaultDCPSource() {
		t.Errorf("default source %q", mon.Config().DCP.Source)
	}
	// Not set up yet: a clear message.
	mon.Update(func(c *Config) error { c.DCP.Source = "agent"; return nil })
	if _, body := do("GET", "/api/dcp/adapters", ""); !strings.Contains(body, "no Linux agent set up") {
		t.Errorf("unconfigured agent: %s", body)
	}
	// Address without http:// and port is completed; wrong token is explained.
	addr := strings.TrimPrefix(srv.URL, "http://")
	if c, body := do("PUT", "/api/dcp/settings", `{"source":"agent","agent_url":"`+addr+`","agent_token":"wrong"}`); c != 200 {
		t.Fatalf("settings: %d %s", c, body)
	}
	if mon.Config().DCP.AgentURL != srv.URL {
		t.Errorf("agent url %q", mon.Config().DCP.AgentURL)
	}
	if _, body := do("GET", "/api/dcp/adapters", ""); !strings.Contains(body, "refused the token") {
		t.Errorf("wrong token: %s", body)
	}
	do("PUT", "/api/dcp/settings", `{"agent_token":"`+token+`"}`)
	if _, body := do("GET", "/api/dcp/adapters", ""); !strings.Contains(body, `"available":true`) || !strings.Contains(body, `"eth0"`) || !strings.Contains(body, `"has_token":true`) || strings.Contains(body, token) {
		t.Errorf("adapters through agent: %s", body)
	}
	if c, body := do("POST", "/api/dcp/identify", `{"adapter":"eth0","wait_ms":1000}`); c != 200 || !strings.Contains(body, "plc-line1") {
		t.Errorf("identify through agent: %d %s", c, body)
	}
	if c, body := do("POST", "/api/dcp/set", `{"adapter":"eth0","mac":"28:63:36:01:02:03","set_name":true,"name":"plc-9","permanent":true}`); c != 200 {
		t.Errorf("set through agent: %d %s", c, body)
	}
	if !bytes.Contains(link.sent[len(link.sent)-1], []byte("plc-9")) {
		t.Error("the agent did not send the name to the device")
	}
	link.refuse = true
	if _, body := do("POST", "/api/dcp/set", `{"adapter":"eth0","mac":"28:63:36:01:02:03","set_name":true,"name":"plc-9"}`); !strings.Contains(body, "Linux agent: ") || !strings.Contains(body, "set not possible for local reasons") {
		t.Errorf("refusal through agent: %s", body)
	}
	if c, _ := do("POST", "/api/dcp/flash", `{"adapter":"eth0","mac":"28:63:36:01:02:03"}`); c != 400 { // fake device refuses
		t.Errorf("flash: %d", c)
	}
	for _, a := range gotAuth[len(gotAuth)-3:] {
		if a != "Bearer "+token {
			t.Errorf("request to the agent sent %q", a)
		}
	}
	// Agent down: a clear message.
	srv.Close()
	if _, body := do("GET", "/api/dcp/adapters", ""); !strings.Contains(body, "Linux agent http") {
		t.Errorf("agent down: %s", body)
	}
	if c, _ := do("PUT", "/api/dcp/settings", `{"source":"elsewhere"}`); c != 400 {
		t.Errorf("bad source accepted: %d", c)
	}
}

func TestDCPOperations(t *testing.T) {
	link := &fakeLink{in: make(chan []byte, 8)}
	oldOpen := dcpOpen
	dcpOpen = func(string) (dcpLink, error) { return link, nil }
	defer func() { dcpOpen = oldOpen }()

	devs, err := dcpIdentify("eth", time.Second)
	if err != nil || len(devs) != 1 || devs[0].Name != "plc-line1" {
		t.Fatalf("identify: %+v %v", devs, err)
	}
	ip, mask, gw, _ := checkIPSettings("192.168.0.20", "255.255.255.0", "")
	if err := dcpSet("eth", plcMAC, []dcpBlock{ipBlock(ip, mask, gw, true)}); err != nil {
		t.Errorf("set: %v", err)
	}
	link.refuse = true
	if err := dcpSet("eth", plcMAC, []dcpBlock{ipBlock(ip, mask, gw, true)}); err == nil || !strings.Contains(err.Error(), "local reasons") {
		t.Errorf("refused set: %v", err)
	}
	if last := link.sent[len(link.sent)-1]; !bytes.Equal(last[0:6], plcMAC) {
		t.Error("set not sent to the device's MAC")
	}
}
