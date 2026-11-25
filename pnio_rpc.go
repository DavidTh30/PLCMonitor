package main

// PROFINET IO: connectionless DCE/RPC (version 4) over UDP port 34964 and the
// PNIO blocks it carries. This is the "context management" (CM) part of an IO
// controller: Connect, Control (PrmEnd / ApplicationReady), Release.
//
// Byte order: the RPC header and the NDR array header follow the sender's
// data representation (we always send little-endian); PNIO blocks are always
// big-endian. See docs/profinet-io-test.md.

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	pnioRPCPort = 34964 // 0x8894

	rpcRequest  = 0
	rpcPing     = 1
	rpcResponse = 2
	rpcFault    = 3
	rpcWorking  = 4
	rpcNoCall   = 5
	rpcReject   = 6

	rpcFlagLastFrag  = 0x02
	rpcFlagFrag      = 0x04
	rpcFlagNoFack    = 0x08
	rpcFlagIdempoten = 0x20

	opConnect = 0
	opRelease = 1
	opRead    = 2
	opWrite   = 3
	opControl = 4

	rpcHeaderLen = 80
	ndrHeaderLen = 20
	argsMaximum  = 1400 // largest response we accept (no fragmentation yet)
)

// uuid16 is a UUID in its canonical (big-endian, as written) byte order.
type uuid16 [16]byte

func mustUUID(s string) uuid16 {
	var u uuid16
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		panic("bad UUID " + s)
	}
	copy(u[:], b)
	return u
}

func newUUID() uuid16 {
	var u uuid16
	rand.Read(u[:])
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return u
}

func (u uuid16) String() string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

var (
	pnioDeviceIf     = mustUUID("DEA00001-6C97-11D1-8271-00A02442DF7D")
	pnioControllerIf = mustUUID("DEA00002-6C97-11D1-8271-00A02442DF7D")
)

// pnioObjectUUID is DEA00000-6C97-11D1-8271-<instance><device id><vendor id>.
func pnioObjectUUID(instance, deviceID, vendorID uint16) uuid16 {
	u := mustUUID("DEA00000-6C97-11D1-8271-000000000000")
	binary.BigEndian.PutUint16(u[10:], instance)
	binary.BigEndian.PutUint16(u[12:], deviceID)
	binary.BigEndian.PutUint16(u[14:], vendorID)
	return u
}

// rpcHeader is the 80-byte connectionless RPC header.
type rpcHeader struct {
	PType, Flags1, Flags2       byte
	LittleEndian                bool
	Object, Interface, Activity uuid16
	ServerBoot, IfVersion, Seq  uint32
	Opnum, Len, FragNum         uint16
}

func putUUID(b []byte, u uuid16, le bool) {
	copy(b, u[:])
	if le {
		b[0], b[1], b[2], b[3] = u[3], u[2], u[1], u[0]
		b[4], b[5] = u[5], u[4]
		b[6], b[7] = u[7], u[6]
	}
}

func getUUID(b []byte, le bool) uuid16 {
	var u uuid16
	copy(u[:], b[:16])
	if le {
		u[0], u[1], u[2], u[3] = b[3], b[2], b[1], b[0]
		u[4], u[5] = b[5], b[4]
		u[6], u[7] = b[7], b[6]
	}
	return u
}

func byteOrder(le bool) binary.ByteOrder {
	if le {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

// encodeRPC builds a datagram: header (little-endian) + body.
func encodeRPC(h rpcHeader, body []byte) []byte {
	b := make([]byte, rpcHeaderLen, rpcHeaderLen+len(body))
	o := binary.LittleEndian
	b[0] = 4 // version
	b[1] = h.PType
	b[2] = h.Flags1
	b[3] = h.Flags2
	b[4] = 0x10 // little-endian integers, ASCII
	b[5] = 0x00 // IEEE floats
	b[6] = 0x00
	b[7] = 0 // serial high
	putUUID(b[8:], h.Object, true)
	putUUID(b[24:], h.Interface, true)
	putUUID(b[40:], h.Activity, true)
	o.PutUint32(b[56:], h.ServerBoot)
	o.PutUint32(b[60:], h.IfVersion)
	o.PutUint32(b[64:], h.Seq)
	o.PutUint16(b[68:], h.Opnum)
	o.PutUint16(b[70:], 0xFFFF) // interface hint
	o.PutUint16(b[72:], 0xFFFF) // activity hint
	o.PutUint16(b[74:], uint16(len(body)))
	o.PutUint16(b[76:], h.FragNum)
	b[78] = 0 // no authentication
	b[79] = 0 // serial low
	return append(b, body...)
}

func decodeRPC(p []byte) (rpcHeader, []byte, error) {
	var h rpcHeader
	if len(p) < rpcHeaderLen || p[0] != 4 {
		return h, nil, errors.New("not a DCE/RPC v4 datagram")
	}
	h.PType, h.Flags1, h.Flags2 = p[1], p[2], p[3]
	h.LittleEndian = p[4]&0x10 != 0
	o := byteOrder(h.LittleEndian)
	h.Object = getUUID(p[8:], h.LittleEndian)
	h.Interface = getUUID(p[24:], h.LittleEndian)
	h.Activity = getUUID(p[40:], h.LittleEndian)
	h.ServerBoot = o.Uint32(p[56:])
	h.IfVersion = o.Uint32(p[60:])
	h.Seq = o.Uint32(p[64:])
	h.Opnum = o.Uint16(p[68:])
	h.Len = o.Uint16(p[74:])
	h.FragNum = o.Uint16(p[76:])
	if h.Flags1&rpcFlagFrag != 0 {
		return h, nil, errors.New("fragmented RPC is not supported yet")
	}
	body := p[rpcHeaderLen:]
	if int(h.Len) > len(body) {
		return h, nil, errors.New("RPC body shorter than its length field")
	}
	return h, body[:h.Len], nil
}

// ndrRequest wraps PNIO blocks for a request: ArgsMaximum, ArgsLength,
// MaximumCount, Offset, ActualCount (little-endian, our data representation).
func ndrRequest(blocks []byte) []byte {
	b := make([]byte, ndrHeaderLen, ndrHeaderLen+len(blocks))
	o := binary.LittleEndian
	o.PutUint32(b[0:], argsMaximum)
	o.PutUint32(b[4:], uint32(len(blocks)))
	o.PutUint32(b[8:], argsMaximum)
	o.PutUint32(b[12:], 0)
	o.PutUint32(b[16:], uint32(len(blocks)))
	return append(b, blocks...)
}

// ndrResponse: PNIOStatus, ArgsLength, MaximumCount, Offset, ActualCount.
func ndrResponse(status [4]byte, maxCount uint32, blocks []byte) []byte {
	b := make([]byte, ndrHeaderLen, ndrHeaderLen+len(blocks))
	copy(b, status[:])
	o := binary.LittleEndian
	o.PutUint32(b[4:], uint32(len(blocks)))
	o.PutUint32(b[8:], maxCount)
	o.PutUint32(b[12:], 0)
	o.PutUint32(b[16:], uint32(len(blocks)))
	return append(b, blocks...)
}

// parseNDRRequest returns ArgsMaximum and the blocks of a request body.
func parseNDRRequest(body []byte, le bool) (uint32, []byte, error) {
	if len(body) < ndrHeaderLen {
		return 0, nil, errors.New("short NDR request")
	}
	o := byteOrder(le)
	n := o.Uint32(body[16:])
	if int(n) > len(body)-ndrHeaderLen {
		return 0, nil, errors.New("NDR length beyond the datagram")
	}
	return o.Uint32(body[0:]), body[ndrHeaderLen : ndrHeaderLen+int(n)], nil
}

// parseNDRResponse returns the PNIOStatus and the blocks of a response body.
func parseNDRResponse(body []byte, le bool) ([4]byte, []byte, error) {
	var st [4]byte
	if len(body) < ndrHeaderLen {
		if len(body) >= 4 {
			copy(st[:], body)
			return st, nil, nil
		}
		return st, nil, errors.New("short NDR response")
	}
	copy(st[:], body)
	n := byteOrder(le).Uint32(body[16:])
	if int(n) > len(body)-ndrHeaderLen {
		n = uint32(len(body) - ndrHeaderLen)
	}
	return st, body[ndrHeaderLen : ndrHeaderLen+int(n)], nil
}

// ---- PNIO blocks (big-endian) ----

// appendBlock adds BlockType, BlockLength (counts from the version bytes on),
// BlockVersion 1.0 and the body.
func appendBlock(b []byte, typ uint16, body []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, typ)
	b = binary.BigEndian.AppendUint16(b, uint16(len(body)+2))
	b = append(b, 1, 0)
	return append(b, body...)
}

type pnioBlock struct {
	Type uint16
	Body []byte // after the version bytes
}

func splitBlocks(p []byte) ([]pnioBlock, error) {
	var out []pnioBlock
	for len(p) > 0 {
		if len(p) < 6 {
			return out, errors.New("truncated PNIO block header")
		}
		typ := binary.BigEndian.Uint16(p)
		n := int(binary.BigEndian.Uint16(p[2:]))
		if n < 2 || 4+n > len(p) {
			return out, fmt.Errorf("PNIO block 0x%04x: bad length %d", typ, n)
		}
		out = append(out, pnioBlock{typ, p[6 : 4+n]})
		p = p[4+n:]
	}
	return out, nil
}

// beReader reads big-endian fields and remembers whether it ran short.
type beReader struct {
	b   []byte
	bad bool
}

func (r *beReader) take(n int) []byte {
	if len(r.b) < n {
		r.bad, r.b = true, nil
		return make([]byte, n)
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}
func (r *beReader) u8() byte    { return r.take(1)[0] }
func (r *beReader) u16() uint16 { return binary.BigEndian.Uint16(r.take(2)) }
func (r *beReader) u32() uint32 { return binary.BigEndian.Uint32(r.take(4)) }
func (r *beReader) uuid() uuid16 {
	var u uuid16
	copy(u[:], r.take(16))
	return u
}

// ---- PNIOStatus ----

func pnioStatusOK(s [4]byte) bool { return s == [4]byte{} }

// pnioStatusText explains a PNIOStatus (ErrorCode, ErrorDecode, ErrorCode1,
// ErrorCode2) as far as an IO test needs it.
func pnioStatusText(s [4]byte) string {
	if pnioStatusOK(s) {
		return "OK"
	}
	t := fmt.Sprintf("PNIOStatus %02X %02X %02X %02X", s[0], s[1], s[2], s[3])
	services := map[byte]string{0xDB: "connect", 0xDC: "release", 0xDD: "control", 0xDE: "read", 0xDF: "write"}
	if v, ok := services[s[0]]; ok {
		t += " " + v
	}
	if s[1] != 0x81 { // 0x80 = PNIORW (record errors), others are rare here
		return t
	}
	blocks := map[byte]string{
		1: "faulty ARBlockReq", 2: "faulty IOCRBlockReq", 3: "faulty ExpectedSubmoduleBlockReq",
		4: "faulty AlarmCRBlockReq", 5: "faulty PrmServerBlockReq", 6: "faulty MCRBlockReq",
		7: "faulty ARRPCBlockReq", 20: "faulty IODControlReq", 21: "faulty IOXControlReq",
		60: "AlarmAck error", 61: "CMDEV error", 62: "CMCTL error", 63: "CTLDINA error", 64: "CMRPC error",
	}
	if v, ok := blocks[s[2]]; ok {
		t += ": " + v
		if s[2] <= 21 {
			t += fmt.Sprintf(" (field %d)", s[3])
		}
	}
	if s[2] == 64 {
		switch s[3] {
		case 4:
			t += " – out of AR resources: another controller is probably connected"
		case 5:
			t += " – AR UUID unknown"
		case 6:
			t += " – state conflict"
		}
	}
	return t
}
