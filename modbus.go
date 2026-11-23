package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"

	"github.com/simonvetter/modbus"
)

type modbusPLC struct {
	client      *modbus.ModbusClient
	defaultUnit int
	unit        int  // unit ID the client is currently set to
	bus         bool // RS485 (serial or via a gateway): several slaves share one line
}

// ModbusDialer returns a Dialer for a Modbus device over TCP/IP, over an
// RS485/RS232 serial port (RTU), or over RS485 behind an Ethernet gateway
// that passes RTU frames through TCP (RTU over TCP).
func ModbusDialer(c ModbusConfig) Dialer {
	return func() (PLC, error) {
		if msg := missingTarget(&Config{Protocol: ProtoModbus, Modbus: c}); msg != "" {
			return nil, errors.New(msg)
		}
		conf := &modbus.ClientConfiguration{
			Timeout: c.Timeout,
			Logger:  log.New(io.Discard, "", 0), // errors are reported by the monitor
		}
		switch c.Mode {
		case "rtu":
			conf.URL = "rtu://" + c.SerialPort
			conf.Speed = uint(c.Baud)
			conf.DataBits = uint(c.DataBits)
			conf.StopBits = uint(c.StopBits)
			conf.Parity = map[string]uint{"N": modbus.PARITY_NONE, "E": modbus.PARITY_EVEN, "O": modbus.PARITY_ODD}[c.Parity]
		case "rtuovertcp":
			conf.URL = "rtuovertcp://" + withPort(c.Address, "502")
			conf.Speed = uint(c.Baud) // only used for RTU frame timing
		default:
			conf.URL = "tcp://" + withPort(c.Address, "502")
		}
		cl, err := modbus.NewClient(conf)
		if err != nil {
			return nil, fmt.Errorf("modbus %s: %w", conf.URL, err)
		}
		if err := cl.Open(); err != nil {
			return nil, fmt.Errorf("connect %s: %w", modbusTarget(c), err)
		}
		cl.SetUnitId(uint8(c.UnitID))
		return &modbusPLC{client: cl, defaultUnit: c.UnitID, unit: c.UnitID, bus: c.Mode != "tcp"}, nil
	}
}

func withPort(addr, port string) string {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(strings.Trim(addr, "[]"), port)
	}
	return addr
}

func modbusTarget(c ModbusConfig) string {
	switch c.Mode {
	case "rtu":
		return fmt.Sprintf("Modbus RTU %s %d %d%s%d unit %d", c.SerialPort, c.Baud, c.DataBits, c.Parity, c.StopBits, c.UnitID)
	case "rtuovertcp":
		return fmt.Sprintf("Modbus RTU over TCP %s unit %d", c.Address, c.UnitID)
	}
	return fmt.Sprintf("Modbus TCP %s unit %d", c.Address, c.UnitID)
}

func (p *modbusPLC) ReadBlock(b *Block, buf []byte) error {
	unit := p.defaultUnit
	if b.Unit > 0 {
		unit = b.Unit
	}
	if unit != p.unit {
		p.client.SetUnitId(uint8(unit))
		p.unit = unit
	}
	var err error
	switch b.Area {
	case AreaHR, AreaIR:
		kind := modbus.HOLDING_REGISTER
		if b.Area == AreaIR {
			kind = modbus.INPUT_REGISTER
		}
		var regs []uint16
		regs, err = p.client.ReadRegisters(uint16(b.Start/2), uint16(b.Size/2), kind)
		if err == nil && len(regs) != b.Size/2 {
			err = fmt.Errorf("got %d registers, want %d", len(regs), b.Size/2)
		}
		for i, r := range regs {
			binary.BigEndian.PutUint16(buf[2*i:], r)
		}
	case AreaCoil, AreaDI:
		var bits []bool
		if b.Area == AreaCoil {
			bits, err = p.client.ReadCoils(uint16(b.Start), uint16(b.Size))
		} else {
			bits, err = p.client.ReadDiscreteInputs(uint16(b.Start), uint16(b.Size))
		}
		if err == nil && len(bits) != b.Size {
			err = fmt.Errorf("got %d bits, want %d", len(bits), b.Size)
		}
		for i, v := range bits {
			buf[i] = 0
			if v {
				buf[i] = 1
			}
		}
	default:
		err = fmt.Errorf("area %v is not a Modbus area", b.Area)
	}
	if err != nil {
		err = fmt.Errorf("read %s: %w", blockName(b), err)
		switch {
		case isAnyOf(err, modbus.ErrIllegalFunction, modbus.ErrIllegalDataAddress,
			modbus.ErrIllegalDataValue, modbus.ErrServerDeviceFailure, modbus.ErrServerDeviceBusy,
			modbus.ErrAcknowledge, modbus.ErrMemoryParityError,
			modbus.ErrGWPathUnavailable, modbus.ErrGWTargetFailedToRespond):
			// The device (or gateway) answered, so the link is fine: only
			// this block is bad.
			return &blockError{err: err}
		case p.bus && errors.Is(err, modbus.ErrRequestTimedOut):
			// On an RS485 bus one slave not answering doesn't mean the line
			// is down. The monitor reconnects only if no block gets a reply.
			return &blockError{err: err, noReply: true}
		case p.bus && isAnyOf(err, modbus.ErrBadCRC, modbus.ErrShortFrame, modbus.ErrBadUnitId, modbus.ErrProtocolError):
			// Line noise or a garbled reply; the next poll usually works.
			return &blockError{err: err}
		}
		return err
	}
	return nil
}

func (p *modbusPLC) WriteTag(t *Tag, v any) error {
	unit := p.defaultUnit
	if t.Unit > 0 {
		unit = t.Unit
	}
	if unit != p.unit {
		p.client.SetUnitId(uint8(unit))
		p.unit = unit
	}
	var err error
	switch {
	case t.area == AreaCoil:
		err = p.client.WriteCoil(uint16(t.Offset), v.(bool))
	case t.area == AreaHR && t.dtype == TypeBool:
		// One bit of a register: read it, change the bit, write it back.
		// (Another writer changing the same register in between would be
		// overwritten; Modbus has no plain bit write for registers.)
		var r uint16
		r, err = p.client.ReadRegister(uint16(t.Offset), modbus.HOLDING_REGISTER)
		if err == nil {
			if v.(bool) {
				r |= 1 << uint(t.Bit)
			} else {
				r &^= 1 << uint(t.Bit)
			}
			err = p.client.WriteRegister(uint16(t.Offset), r)
		}
	case t.area == AreaHR:
		_, data := tagBytes(t, v)
		regs := make([]uint16, len(data)/2)
		for i := range regs {
			regs[i] = binary.BigEndian.Uint16(data[2*i:])
		}
		if len(regs) == 1 {
			err = p.client.WriteRegister(uint16(t.Offset), regs[0])
		} else {
			err = p.client.WriteRegisters(uint16(t.Offset), regs)
		}
	default:
		err = fmt.Errorf("%s is read-only in Modbus", t.area)
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", t.Address(), err)
	}
	return nil
}

// ProbeUnit asks one slave ID for holding register 0. Any answer, including
// an exception such as "illegal data address", means a device is there; a
// timeout means nobody answered. Other errors mean the link itself failed.
func (p *modbusPLC) ProbeUnit(unit int) (bool, error) {
	if unit != p.unit {
		p.client.SetUnitId(uint8(unit))
		p.unit = unit
	}
	_, err := p.client.ReadRegisters(0, 1, modbus.HOLDING_REGISTER)
	switch {
	case err == nil, isAnyOf(err, modbus.ErrIllegalFunction, modbus.ErrIllegalDataAddress,
		modbus.ErrIllegalDataValue, modbus.ErrServerDeviceFailure, modbus.ErrServerDeviceBusy, modbus.ErrAcknowledge):
		return true, nil
	case isAnyOf(err, modbus.ErrRequestTimedOut, modbus.ErrGWTargetFailedToRespond, modbus.ErrGWPathUnavailable,
		modbus.ErrBadCRC, modbus.ErrShortFrame, modbus.ErrBadUnitId):
		return false, nil
	}
	return false, err
}

func isAnyOf(err error, targets ...error) bool {
	for _, e := range targets {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

func (p *modbusPLC) Close() error { return p.client.Close() }
