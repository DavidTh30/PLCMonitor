package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// crc16 is the Modbus RTU CRC (poly 0xA001, init 0xFFFF).
func crc16(b []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, c := range b {
		crc ^= uint16(c)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

type rtuSlave struct {
	hr   [100]uint16
	coil [100]bool
}

// fakeRS485Bus answers RTU frames over TCP, like an RS485-to-Ethernet
// gateway in transparent mode with several slaves on the line. Slaves not
// in the map stay silent, as a dead or missing device on a real bus does.
type fakeRS485Bus struct {
	mu     sync.Mutex
	slaves map[byte]*rtuSlave
}

func (bus *fakeRS485Bus) serve(t *testing.T, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			req := make([]byte, 8) // unit, fc, addr(2), qty(2), crc(2)
			for {
				if _, err := io.ReadFull(c, req); err != nil {
					return
				}
				if crc16(req[:6]) != binary.LittleEndian.Uint16(req[6:]) {
					t.Errorf("request with bad CRC: % x", req)
					return
				}
				bus.mu.Lock()
				s := bus.slaves[req[0]]
				bus.mu.Unlock()
				if s == nil {
					continue // nobody home: no reply
				}
				fc, addr, qty := req[1], int(binary.BigEndian.Uint16(req[2:])), int(binary.BigEndian.Uint16(req[4:]))
				resp := []byte{req[0], fc}
				switch fc {
				case 3: // read holding registers
					resp = append(resp, byte(2*qty))
					for i := 0; i < qty; i++ {
						resp = binary.BigEndian.AppendUint16(resp, s.hr[addr+i])
					}
				case 1: // read coils
					data := make([]byte, (qty+7)/8)
					for i := 0; i < qty; i++ {
						if s.coil[addr+i] {
							data[i/8] |= 1 << (i % 8)
						}
					}
					resp = append(resp, byte(len(data)))
					resp = append(resp, data...)
				default:
					resp = []byte{req[0], fc | 0x80, 1} // illegal function
				}
				resp = binary.LittleEndian.AppendUint16(resp, crc16(resp))
				c.Write(resp)
			}
		}()
	}
}

func startRS485Bus(t *testing.T, slaves map[byte]*rtuSlave) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go (&fakeRS485Bus{slaves: slaves}).serve(t, ln)
	return ln.Addr().String()
}

func rs485Config(addr string, tags []Tag) *Config {
	cfg := &Config{
		Protocol:     ProtoModbus,
		Modbus:       ModbusConfig{Mode: "rtuovertcp", Address: addr, UnitID: 1, Baud: 19200, Timeout: 150 * time.Millisecond},
		PollInterval: 50 * time.Millisecond, ReconnectDelay: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond,
		Tags: tags,
	}
	cfg.applyDefaults()
	return cfg
}

// TestRS485MultiSlave polls three slaves on one RTU line; slave 9 never
// answers. Its tags go bad while the others keep updating and the link
// stays up.
func TestRS485MultiSlave(t *testing.T) {
	s1, s2 := &rtuSlave{}, &rtuSlave{}
	s1.hr[0] = uint16(0xFFFF &^ 4) // INT -5
	s2.hr[0] = 42
	s2.hr[5], s2.hr[6] = 0x0001, 0x86A0 // DINT 100000
	s2.coil[1] = true
	addr := startRS485Bus(t, map[byte]*rtuSlave{1: s1, 2: s2})

	cfg := rs485Config(addr, []Tag{
		{Name: "s1_temp", Area: "HR", Offset: 0, Type: "INT"}, // default unit 1
		{Name: "s2_level", Area: "HR", Offset: 0, Type: "INT", Unit: 2},
		{Name: "s2_total", Area: "HR", Offset: 5, Type: "DINT", Unit: 2},
		{Name: "s2_run", Area: "COIL", Offset: 1, Type: "BOOL", Unit: 2},
		{Name: "s9_dead", Area: "HR", Offset: 0, Type: "INT", Unit: 9},
	})
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	if a := cfg.Tags[1].Address(); a != "U2:HR0" {
		t.Errorf("address %q", a)
	}
	mon := NewMonitor(cfg, func(c *Config) Dialer { return ModbusDialer(c.Modbus) }, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mon.Run(ctx)

	want := map[string]string{"s1_temp": "-5", "s2_level": "42", "s2_total": "100000", "s2_run": "TRUE"}
	waitFor(t, "values from slaves 1 and 2", func() bool {
		_, tags := mon.Snapshot()
		for _, s := range tags {
			if w, ok := want[s.Name]; ok && (!s.Good || s.Display != w) {
				return false
			}
		}
		return true
	})
	waitFor(t, "slave 9 marked bad", func() bool {
		_, tags := mon.Snapshot()
		return strings.Contains(tags[4].Error, "timed out") && strings.Contains(tags[4].Error, "unit 9")
	})
	time.Sleep(400 * time.Millisecond) // several more polls with slave 9 silent
	st, tags := mon.Snapshot()
	if !st.Connected || st.Reconnects != 0 {
		t.Errorf("one silent slave dropped the link: connected=%v reconnects=%d", st.Connected, st.Reconnects)
	}
	if !tags[0].Good || !tags[1].Good {
		t.Error("healthy slaves went bad")
	}
}

// TestRS485AllSilent: when no slave answers at all, the line itself is
// treated as down and the monitor reconnects.
func TestRS485AllSilent(t *testing.T) {
	addr := startRS485Bus(t, map[byte]*rtuSlave{})
	cfg := rs485Config(addr, []Tag{{Name: "x", Area: "HR", Offset: 0, Type: "INT", Unit: 3}})
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	mon := NewMonitor(cfg, func(c *Config) Dialer { return ModbusDialer(c.Modbus) }, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mon.Run(ctx)
	waitFor(t, "line reported down", func() bool {
		st, _ := mon.Snapshot()
		return !st.Connected && st.Reconnects > 0 && strings.Contains(st.LastError, "no device answered")
	})
}

func TestModbusModeValidation(t *testing.T) {
	// A missing address is accepted (the simulator can be switched off before
	// the address is known), but nothing is dialed and the reason is given.
	cfg := &Config{Protocol: ProtoModbus, Modbus: ModbusConfig{Mode: "rtuovertcp"}}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Errorf("empty gateway address refused: %v", err)
	}
	if _, err := ModbusDialer(cfg.Modbus)(); err == nil || !strings.Contains(err.Error(), "no gateway address set") {
		t.Errorf("dial without gateway address: %v", err)
	}
	if _, err := S7Dialer(PLCConfig{ConnectionType: "pg"})(); err == nil || !strings.Contains(err.Error(), "no PLC address set") {
		t.Errorf("dial without PLC address: %v", err)
	}
	if got := targetName(&Config{Protocol: ProtoS7}); got != "ISO-TCP – no address set" {
		t.Errorf("target name: %q", got)
	}
	cfg.Modbus.Mode = "bogus"
	if err := cfg.validate(); err == nil {
		t.Error("unknown Modbus mode accepted")
	}
	bad := Tag{Name: "x", Area: "HR", Type: "INT", Unit: 300}
	if err := bad.normalize(ProtoModbus); err == nil {
		t.Error("slave ID 300 accepted")
	}
	s7 := Tag{Name: "x", Area: "M", Type: "INT", Unit: 5}
	if err := s7.normalize(ProtoS7); err != nil || s7.Unit != 0 {
		t.Errorf("S7 tag kept a slave ID: %v %d", err, s7.Unit)
	}
}
