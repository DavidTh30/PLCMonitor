package main

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/simonvetter/modbus"
)

// fakeDevice is a Modbus server image: registers below 500 and coils/inputs
// below 100 exist; anything else answers "illegal data address".
type fakeDevice struct {
	mu       sync.Mutex
	hr, ir   [500]uint16
	coil, di [100]bool
}

func (d *fakeDevice) HandleCoils(r *modbus.CoilsRequest) ([]bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if int(r.Addr)+int(r.Quantity) > len(d.coil) {
		return nil, modbus.ErrIllegalDataAddress
	}
	if r.IsWrite {
		copy(d.coil[r.Addr:], r.Args)
	}
	return append([]bool(nil), d.coil[r.Addr:int(r.Addr)+int(r.Quantity)]...), nil
}

func (d *fakeDevice) HandleDiscreteInputs(r *modbus.DiscreteInputsRequest) ([]bool, error) {
	if int(r.Addr)+int(r.Quantity) > len(d.di) {
		return nil, modbus.ErrIllegalDataAddress
	}
	return append([]bool(nil), d.di[r.Addr:int(r.Addr)+int(r.Quantity)]...), nil
}

func (d *fakeDevice) HandleHoldingRegisters(r *modbus.HoldingRegistersRequest) ([]uint16, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if int(r.Addr)+int(r.Quantity) > len(d.hr) {
		return nil, modbus.ErrIllegalDataAddress
	}
	if r.IsWrite {
		copy(d.hr[r.Addr:], r.Args)
	}
	return append([]uint16(nil), d.hr[r.Addr:int(r.Addr)+int(r.Quantity)]...), nil
}

func (d *fakeDevice) HandleInputRegisters(r *modbus.InputRegistersRequest) ([]uint16, error) {
	if int(r.Addr)+int(r.Quantity) > len(d.ir) {
		return nil, modbus.ErrIllegalDataAddress
	}
	return append([]uint16(nil), d.ir[r.Addr:int(r.Addr)+int(r.Quantity)]...), nil
}

func freePort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// TestModbusTCP polls a real Modbus TCP server (the library's own, on
// loopback) through ModbusDialer and checks every area and data type.
func TestModbusTCP(t *testing.T) {
	dev := &fakeDevice{}
	dev.hr[0] = uint16(0xFFFF &^ 1233) // INT -1234
	f := math.Float32bits(12.5)
	dev.hr[2], dev.hr[3] = uint16(f>>16), uint16(f) // REAL, high word first
	dev.hr[10] = 0x8001                             // bits 0 and 15
	dev.hr[20], dev.hr[21] = 0xFFFF, 0xFFFE         // DINT -2
	dev.ir[5] = 777
	dev.coil[3] = true
	dev.di[7] = true

	port := freePort(t)
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: "tcp://127.0.0.1:" + port, Timeout: 5 * time.Second, MaxClients: 2}, dev)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	cfg := &Config{
		Protocol:     ProtoModbus,
		Modbus:       ModbusConfig{Mode: "tcp", Address: "127.0.0.1:" + port, UnitID: 1},
		PollInterval: 50 * time.Millisecond, ReconnectDelay: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond,
		Tags: []Tag{
			{Name: "int", Area: "HR", Offset: 0, Type: "INT"},
			{Name: "real", Area: "HR", Offset: 2, Type: "REAL"},
			{Name: "bit0", Area: "HR", Offset: 10, Bit: 0, Type: "BOOL"},
			{Name: "bit1", Area: "4x", Offset: 10, Bit: 1, Type: "BOOL"},
			{Name: "bit15", Area: "HR", Offset: 10, Bit: 15, Type: "BOOL"},
			{Name: "dint", Area: "HR", Offset: 20, Type: "DINT"},
			{Name: "ir", Area: "IR", Offset: 5, Type: "WORD"},
			{Name: "coil", Area: "COIL", Offset: 3, Type: "BOOL"},
			{Name: "di", Area: "DI", Offset: 7, Type: "BOOL"},
			{Name: "missing", Area: "HR", Offset: 900, Type: "INT"}, // device rejects this
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	mon := NewMonitor(cfg, func(c *Config) Dialer { return ModbusDialer(c.Modbus) }, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mon.Run(ctx)

	want := map[string]string{
		"int": "-1234", "real": "12.5", "bit0": "TRUE", "bit1": "FALSE", "bit15": "TRUE",
		"dint": "-2", "ir": "777", "coil": "TRUE", "di": "TRUE",
	}
	waitFor(t, "modbus values", func() bool {
		_, tags := mon.Snapshot()
		for _, s := range tags {
			if w, ok := want[s.Name]; ok && (!s.Good || s.Display != w) {
				return false
			}
		}
		return true
	})
	st, tags := mon.Snapshot()
	if !st.Connected {
		t.Error("illegal-address reply on one tag dropped the connection")
	}
	for _, s := range tags {
		if s.Name == "missing" && (s.Good || !strings.Contains(s.Error, "illegal data address")) {
			t.Errorf("missing register: good=%v error=%q", s.Good, s.Error)
		}
	}
	if st.Reconnects != 0 {
		t.Errorf("reconnected %d times", st.Reconnects)
	}
}

func TestModbusWordOrderAndAddress(t *testing.T) {
	buf := make([]byte, 8)
	// REAL 12.5 with the low word first (CDAB), as some devices send it.
	f := math.Float32bits(12.5)
	binary.BigEndian.PutUint16(buf[0:], uint16(f))
	binary.BigEndian.PutUint16(buf[2:], uint16(f>>16))
	cfg := &Config{Protocol: ProtoModbus, Modbus: ModbusConfig{WordOrder: "little", Address: "x"},
		Tags: []Tag{{Name: "r", Area: "HR", Offset: 0, Type: "REAL"}, {Name: "b", Area: "HR", Offset: 3, Bit: 9, Type: "BOOL"}}}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if v, err := cfg.Tags[0].Decode(buf, 0); err != nil || v != 12.5 {
		t.Errorf("little word order REAL: %v %v", v, err)
	}
	out := make([]byte, 8)
	cfg.Tags[0].Encode(out, 12.5)
	if string(out[:4]) != string(buf[:4]) {
		t.Error("encode does not mirror word order")
	}
	if a := cfg.Tags[1].Address(); a != "HR3.9" {
		t.Errorf("address %s", a)
	}

	bad := []Tag{
		{Name: "x", Area: "COIL", Offset: 0, Type: "INT"},
		{Name: "x", Area: "HR", Offset: 0, Type: "BYTE"},
		{Name: "x", Area: "HR", Offset: 0, Bit: 16, Type: "BOOL"},
		{Name: "x", Area: "HR", Offset: 65535, Type: "DINT"},
	}
	for _, tg := range bad {
		if err := tg.normalize(ProtoModbus); err == nil {
			t.Errorf("accepted invalid tag %+v", tg)
		}
	}
	// A tag of the other protocol is kept but not polled.
	s7 := Tag{Name: "s7", Area: "DB", DB: 1, Type: "INT"}
	if err := s7.normalize(ProtoModbus); err != nil || s7.inactive == "" {
		t.Errorf("S7 tag under Modbus: err=%v inactive=%q", err, s7.inactive)
	}
	if len(BuildBlocks([]Tag{s7}, 0)) != 0 {
		t.Error("inactive tag was polled")
	}
}

func TestModbusBlocks(t *testing.T) {
	tags := []Tag{
		{Name: "a", Area: "HR", Offset: 0, Type: "INT"},
		{Name: "b", Area: "HR", Offset: 1, Type: "REAL"},  // adjacent: same block
		{Name: "c", Area: "HR", Offset: 5, Type: "INT"},   // 2-register gap
		{Name: "d", Area: "HR", Offset: 200, Type: "INT"}, // past 125 registers
	}
	for i := range tags {
		if err := tags[i].normalize(ProtoModbus); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(BuildBlocks(tags, 0)); n != 3 {
		t.Errorf("max_gap 0: %d blocks, want 3", n)
	}
	blocks := BuildBlocks(tags, 2)
	if len(blocks) != 2 || blocks[0].Start != 0 || blocks[0].Size != 12 {
		t.Errorf("max_gap 2: %+v", blocks[0])
	}
}
