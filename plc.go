package main

import (
	"errors"
	"fmt"

	"github.com/robinson/gos7"
)

// PLC is the interface the monitor needs from a connection. It is
// implemented by the S7 and Modbus connections and by the simulator.
type PLC interface {
	ReadBlock(b *Block, buf []byte) error
	// WriteTag writes one value (bool, int64 or float64, already range
	// checked) to the tag's address.
	WriteTag(t *Tag, v any) error
	Close() error
}

// tagBytes encodes v as the bytes of the tag's image range, the same layout
// the tag is read in.
func tagBytes(t *Tag, v any) (start int, data []byte) {
	start, end := t.imageRange()
	buf := make([]byte, end)
	t.Encode(buf, v)
	return start, buf[start:end]
}

// Dialer opens a new PLC connection.
type Dialer func() (PLC, error)

// blockError is a read error that affects only one block (for example a
// Modbus "illegal data address" reply); the connection itself is still good.
type blockError struct {
	err     error
	noReply bool // the device did not answer at all (vs. answered with an error)
}

func (e *blockError) Error() string { return e.err.Error() }
func (e *blockError) Unwrap() error { return e.err }

type s7PLC struct {
	handler *gos7.TCPClientHandler
	client  gos7.Client
}

// S7Dialer returns a Dialer that connects over ISO-on-TCP (port 102).
func S7Dialer(c PLCConfig) Dialer {
	return func() (PLC, error) {
		if msg := missingTarget(&Config{Protocol: ProtoS7, PLC: c}); msg != "" {
			return nil, errors.New(msg) // never dial ":102" (this PC)
		}
		ct, err := connectionTypeCode(c.ConnectionType)
		if err != nil {
			return nil, err
		}
		h := gos7.NewTCPClientHandlerWithConnectType(c.Address, c.Rack, c.Slot, ct)
		h.Timeout = c.Timeout
		h.IdleTimeout = 0 // we poll continuously; let reconnect logic own the socket
		if err := h.Connect(); err != nil {
			return nil, fmt.Errorf("connect %s rack %d slot %d: %w", c.Address, c.Rack, c.Slot, err)
		}
		return &s7PLC{handler: h, client: gos7.NewClient(h)}, nil
	}
}

func (p *s7PLC) ReadBlock(b *Block, buf []byte) error {
	var err error
	switch b.Area {
	case AreaDB:
		err = p.client.AGReadDB(b.DB, b.Start, b.Size, buf)
	case AreaI:
		err = p.client.AGReadEB(b.Start, b.Size, buf)
	case AreaQ:
		err = p.client.AGReadAB(b.Start, b.Size, buf)
	case AreaM:
		err = p.client.AGReadMB(b.Start, b.Size, buf)
	default:
		err = fmt.Errorf("unsupported area %v", b.Area)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", blockName(b), err)
	}
	return nil
}

// S7 area codes as used in the S7 protocol.
var s7AreaCode = map[Area]int{AreaI: 0x81, AreaQ: 0x82, AreaM: 0x83, AreaDB: 0x84}

func (p *s7PLC) WriteTag(t *Tag, v any) error {
	var err error
	if t.dtype == TypeBool {
		// Write the single bit (transport size BIT), so the PLC changes only
		// that bit and not the other seven in the byte.
		var b byte
		if v.(bool) {
			b = 1
		}
		item := []gos7.S7DataItem{{Area: s7AreaCode[t.area], WordLen: 0x01, DBNumber: t.DB,
			Start: t.Offset*8 + t.Bit, Amount: 1, Data: []byte{b}}}
		err = p.client.AGWriteMulti(item, 1)
		if err == nil && item[0].Error != "" {
			err = fmt.Errorf("%s", item[0].Error)
		}
	} else {
		start, data := tagBytes(t, v)
		switch t.area {
		case AreaDB:
			err = p.client.AGWriteDB(t.DB, start, len(data), data)
		case AreaI:
			err = p.client.AGWriteEB(start, len(data), data)
		case AreaQ:
			err = p.client.AGWriteAB(start, len(data), data)
		case AreaM:
			err = p.client.AGWriteMB(start, len(data), data)
		default:
			err = fmt.Errorf("unsupported area %v", t.area)
		}
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", t.Address(), err)
	}
	return nil
}

func (p *s7PLC) Close() error { return p.handler.Close() }

func blockName(b *Block) string {
	unit := ""
	if b.Unit > 0 {
		unit = fmt.Sprintf("unit %d ", b.Unit)
	}
	switch {
	case b.Area.isRegister():
		return fmt.Sprintf("%s%s %d..%d", unit, b.Area, b.Start/2, (b.Start+b.Size)/2-1)
	case b.Area.isBitArea():
		return fmt.Sprintf("%s%s %d..%d", unit, b.Area, b.Start, b.Start+b.Size-1)
	}
	if b.Area == AreaDB {
		return fmt.Sprintf("DB%d bytes %d..%d", b.DB, b.Start, b.Start+b.Size-1)
	}
	return fmt.Sprintf("%s bytes %d..%d", b.Area, b.Start, b.Start+b.Size-1)
}
