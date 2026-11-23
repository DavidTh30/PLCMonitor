package main

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
)

// startUAServer runs an OPC UA server laid out like a SIMATIC S7-1500:
//
//	Objects/PLC_1/Memory/"Start_PB"                       Boolean
//	Objects/PLC_1/Inputs/"Level_Raw"                      Int16
//	Objects/PLC_1/DataBlocksGlobal/"Line_Data"/
//	    "Line_Data"."Speed"                               Int16 (comment "rpm")
//	    "Line_Data"."Temp"                                Float
//	    "Line_Data"."Motor"/"Line_Data"."Motor"."Run"     struct with a Boolean
//	    "Line_Data"."Arr"                                 array of Int16
func startUAServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return startUAServerAt(t, strconv.Itoa(port))
}

func startUAServerAt(t *testing.T, portText string) string {
	t.Helper()
	port, _ := strconv.Atoi(portText)
	s := server.New(server.EndPoint("127.0.0.1", port),
		server.EnableSecurity("None", ua.MessageSecurityModeNone),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous))
	ns := server.NewNodeNameSpace(s, "http://www.siemens.com/simatic-s7-opcua")
	n := ns.ID()
	folder := func(parent *server.Node, sid, name string) *server.Node {
		f := ns.AddNode(server.NewFolderNode(ua.NewStringNodeID(n, sid), name))
		parent.AddRef(f, server.RefTypeIDOrganizes, true)
		return f
	}
	variable := func(parent *server.Node, sid, name string, dt uint32, v any, comment string) *server.Node {
		x := server.NewVariableNode(ua.NewStringNodeID(n, sid), name, v)
		x.SetAttribute(ua.AttributeIDDataType, server.DataValueFromValue(ua.NewNumericNodeID(0, dt)))
		x.SetAttribute(ua.AttributeIDAccessLevel, server.DataValueFromValue(byte(ua.AccessLevelTypeCurrentRead|ua.AccessLevelTypeCurrentWrite)))
		x.SetAttribute(ua.AttributeIDValueRank, server.DataValueFromValue(int32(-1)))
		x.SetAttribute(ua.AttributeIDDescription, server.DataValueFromValue(&ua.LocalizedText{EncodingMask: ua.LocalizedTextText, Text: comment}))
		ns.AddNode(x)
		parent.AddRef(x, server.RefTypeIDHasComponent, true)
		return x
	}
	root := s.Node(ua.NewNumericNodeID(0, id.ObjectsFolder))
	plc := folder(root, "PLC_1", "PLC_1")
	mem := folder(plc, "Memory", "Memory")
	variable(mem, `"Start_PB"`, "Start_PB", id.Boolean, true, "start push button")
	in := folder(plc, "Inputs", "Inputs")
	variable(in, `"Level_Raw"`, "Level_Raw", id.Int16, int16(1234), "")
	dbs := folder(plc, "DataBlocksGlobal", "DataBlocksGlobal")
	db := folder(dbs, `"Line_Data"`, "Line_Data")
	variable(db, `"Line_Data"."Speed"`, "Speed", id.Int16, int16(-42), "rpm")
	variable(db, `"Line_Data"."Temp"`, "Temp", id.Float, float32(21.5), "")
	motor := variable(db, `"Line_Data"."Motor"`, "Motor", 3001, nil, "")
	variable(motor, `"Line_Data"."Motor"."Run"`, "Run", id.Boolean, false, "running")
	arr := variable(db, `"Line_Data"."Arr"`, "Arr", id.Int16, []int16{1, 2, 3}, "")
	arr.SetAttribute(ua.AttributeIDValueRank, server.DataValueFromValue(int32(1)))
	// The device's identity, as OPC UA Devices: Objects/DeviceSet/PLC_1.
	ds := folder(root, "DeviceSet", "DeviceSet")
	dev := folder(ds, "PLC", "PLC_1")
	for _, v := range [][2]string{{"Model", "CPU 1215C DC/DC/DC"}, {"OrderNumber", "6AA0 000-0AA00-0AA0"}, {"SoftwareRevision", "V04.06.01"}, {"SerialNumber", "S X-123"}} {
		variable(dev, v[0], v[0], id.String, v[1], "")
	}
	dev.AddRef(mem, server.RefTypeIDOrganizes, true)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return "opc.tcp://127.0.0.1:" + strconv.Itoa(port)
}

// TestIdentifyUA: what the network scan tells about an OPC UA server.
func TestIdentifyUA(t *testing.T) {
	u := identifyUA(t.Context(), startUAServer(t), 400*time.Millisecond)
	if u.Error != "" || !u.Anonymous || len(u.Security) != 1 || u.Security[0] != "None" {
		t.Fatalf("%+v", u)
	}
	if u.Device != "PLC_1" || u.Model != "CPU 1215C DC/DC/DC" || u.Order != "6AA0 000-0AA00-0AA0" || u.Firmware != "V04.06.01" || u.Serial != "S X-123" {
		t.Errorf("identity: %+v", u)
	}
	if u.Tags != "yes" {
		t.Errorf("tags: %q", u.Tags)
	}

	// A PLC whose OPC UA server publishes no tags (standard interface off).
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s := server.New(server.EndPoint("127.0.0.1", port), server.EnableSecurity("None", ua.MessageSecurityModeNone),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous))
	ns := server.NewNodeNameSpace(s, "http://www.siemens.com/simatic-s7-opcua")
	ds := ns.AddNode(server.NewFolderNode(ua.NewStringNodeID(ns.ID(), "DeviceSet"), "DeviceSet"))
	s.Node(ua.NewNumericNodeID(0, id.ObjectsFolder)).AddRef(ds, server.RefTypeIDOrganizes, true)
	dev := ns.AddNode(server.NewFolderNode(ua.NewStringNodeID(ns.ID(), "PLC"), "PLC_1"))
	ds.AddRef(dev, server.RefTypeIDOrganizes, true)
	m := ns.AddNode(server.NewVariableNode(ua.NewStringNodeID(ns.ID(), "Model"), "Model", "CPU 1217C DC/DC/DC"))
	dev.AddRef(m, server.RefTypeIDHasComponent, true)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	u = identifyUA(t.Context(), "opc.tcp://127.0.0.1:"+strconv.Itoa(port), 400*time.Millisecond)
	if u.Model != "CPU 1217C DC/DC/DC" || u.Tags != "no" {
		t.Errorf("no tags: %+v", u)
	}

	// Nothing listening.
	if u := identifyUA(t.Context(), "opc.tcp://127.0.0.1:1", 200*time.Millisecond); u.Error == "" {
		t.Error("no server: no error")
	}
}

func TestUABrowse(t *testing.T) {
	ep := startUAServer(t)
	b := NewUABrowser()
	t.Cleanup(b.Close)
	if err := b.Start(OPCUAConfig{Endpoint: ep}); err != nil {
		t.Fatal(err)
	}
	var st UABrowseState
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		st = b.State()
		if !st.Running || time.Now().After(deadline) {
			break
		}
	}
	if st.Error != "" {
		t.Fatal(st.Error)
	}
	if st.Hint != "" {
		t.Errorf("hint with tags found: %q", st.Hint)
	}
	m := map[string]UAVar{}
	for _, v := range st.Vars {
		m[v.Name] = v
	}
	check := func(name, folder, typ string) UAVar {
		v, ok := m[name]
		if !ok {
			t.Errorf("%s not found in %v", name, st.Vars)
			return v
		}
		if v.Folder != folder || v.Type != typ {
			t.Errorf("%s: folder %q type %q, want %q %q", name, v.Folder, v.Type, folder, typ)
		}
		return v
	}
	if v := check("Start_PB", "M", "BOOL"); v.Comment != "start push button" || !v.Writable {
		t.Errorf("Start_PB: %+v", v)
	}
	check("Level_Raw", "I", "INT")
	if v := check("Line_Data.Speed", "DB", "INT"); v.Node != `ns=1;s="Line_Data"."Speed"` || v.Comment != "rpm" {
		t.Errorf("Speed: %+v", v)
	}
	check("Line_Data.Temp", "DB", "REAL")
	check("Line_Data.Motor.Run", "DB", "BOOL")
	if v := check("Line_Data.Motor", "DB", ""); !v.Struct {
		t.Errorf("Motor not a struct: %+v", v)
	}
	if v := check("Line_Data.Arr", "DB", ""); !v.Array {
		t.Errorf("Arr not an array: %+v", v)
	}
	vals, err := b.Read([]string{`ns=1;s="Line_Data"."Speed"`, "ns=1;s=nope", "bad id"})
	if err != nil {
		t.Fatal(err)
	}
	if vals[0].Value != int16(-42) || vals[1].Error == "" || vals[2].Error == "" {
		t.Errorf("read: %+v", vals)
	}
}

// TestUAMonitor polls and writes OPC UA tags through the monitor.
func TestUAMonitor(t *testing.T) {
	ep := startUAServer(t)
	cfg := &Config{Protocol: ProtoOPCUA, PollInterval: 50 * time.Millisecond, OPCUA: OPCUAConfig{Endpoint: ep},
		Tags: []Tag{
			{Name: "speed", Area: "UA", Node: `ns=1;s="Line_Data"."Speed"`, Type: "INT"},
			{Name: "temp", Area: "UA", Node: `ns=1;s="Line_Data"."Temp"`, Type: "REAL"},
			{Name: "pb", Area: "UA", Node: `ns=1;s="Start_PB"`, Type: "BOOL"},
			{Name: "gone", Area: "UA", Node: `ns=1;s="Nope"`, Type: "INT"},
			{Name: "s7", Area: "M", Offset: 0, Type: "INT"},
		}}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	mon := NewMonitor(cfg, func(c *Config) Dialer { return OPCUADialer(c.OPCUA) }, nil, "")
	go mon.Run(t.Context())
	waitFor(t, "values", func() bool {
		_, tags := mon.Snapshot()
		return tags[0].Good && tags[1].Good && tags[2].Good
	})
	_, tags := mon.Snapshot()
	if tags[0].Value != int64(-42) || tags[1].Value.(float64) != 21.5 || tags[2].Value != true {
		t.Errorf("values: %v %v %v", tags[0].Value, tags[1].Value, tags[2].Value)
	}
	if tags[3].Good || !strings.Contains(tags[3].Error, "Nope") {
		t.Errorf("missing node: good %v error %q", tags[3].Good, tags[3].Error)
	}
	if !strings.Contains(tags[4].Error, "not polled") {
		t.Errorf("S7 tag under OPC UA: %q", tags[4].Error)
	}
	if tags[0].Address != `ns=1;s="Line_Data"."Speed"` {
		t.Errorf("address: %q", tags[0].Address)
	}
	if err := mon.Write("speed", int64(77)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "written value", func() bool { _, tags := mon.Snapshot(); return tags[0].Value == int64(77) })

	// A UA tag without a node ID is refused.
	bad := &Config{Protocol: ProtoOPCUA, Tags: []Tag{{Name: "x", Area: "UA", Type: "INT"}}}
	bad.applyDefaults()
	if err := bad.validate(); err == nil {
		t.Error("UA tag without node accepted")
	}
}

// TestUAServe runs the fake SIMATIC OPC UA server until stopped, for trying
// the PLC tags window without a PLC (developers):
//
//	$env:PLC_UA_SERVE = "4840"; go test -run TestUAServe -timeout 0
func TestUAServe(t *testing.T) {
	port := os.Getenv("PLC_UA_SERVE")
	if port == "" {
		t.Skip("set PLC_UA_SERVE to a port to run the fake OPC UA server")
	}
	ep := startUAServerAt(t, port)
	t.Logf("fake SIMATIC OPC UA server on %s", ep)
	select {}
}

// TestAddS7 maps browsed names to S7 addresses from imported names.
func TestAddS7(t *testing.T) {
	blocks, _, err := parseFiles([]symFile{{"line.db", []byte(tiaSource)},
		{"tags.csv", []byte("Name;Data Type;Logical Address\nStart_PB;Bool;%M0.3\n")}}, map[string]int{"Line_Data": 7})
	if err != nil {
		t.Fatal(err)
	}
	vars := []UAVar{{Name: "Line_Data.M1.Speed", Type: "INT"}, {Name: "start_pb", Type: "BOOL"}, {Name: "Nope", Type: "INT"}, {Name: "Line_Data.Name"}}
	addS7(vars, SymState{Blocks: blocks})
	if vars[0].S7 == nil || vars[0].S7.Address != "DB7.DBW34" || vars[1].S7 == nil || vars[1].S7.Address != "M0.3" || vars[2].S7 != nil || vars[3].S7 != nil {
		t.Errorf("mapping: %+v %+v %+v %+v", vars[0].S7, vars[1].S7, vars[2].S7, vars[3].S7)
	}
}
