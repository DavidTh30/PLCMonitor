package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/gopcua/opcua/ua"
)

// Protocols.
const (
	ProtoS7     = "s7"
	ProtoModbus = "modbus"
	ProtoOPCUA  = "opcua"
)

// Area is a memory area of the PLC or Modbus device.
type Area int

const (
	AreaDB   Area = iota // S7 data block
	AreaI                // S7 process inputs (I / E / PE)
	AreaQ                // S7 process outputs (Q / A / PA)
	AreaM                // S7 markers / flags (M / MK)
	AreaCoil             // Modbus coils (0x), 1 bit each
	AreaDI               // Modbus discrete inputs (1x), 1 bit each
	AreaIR               // Modbus input registers (3x), 16 bits each
	AreaHR               // Modbus holding registers (4x), 16 bits each
	AreaUA               // OPC UA node, read by its node ID
)

func (a Area) String() string {
	return [...]string{"DB", "I", "Q", "M", "COIL", "DI", "IR", "HR", "UA"}[a]
}

// Protocol returns the protocol the area belongs to.
func (a Area) Protocol() string {
	if a == AreaUA {
		return ProtoOPCUA
	}
	if a >= AreaCoil {
		return ProtoModbus
	}
	return ProtoS7
}

func (a Area) isBitArea() bool  { return a == AreaCoil || a == AreaDI }
func (a Area) isRegister() bool { return a == AreaIR || a == AreaHR }

func parseArea(s string) (Area, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DB":
		return AreaDB, nil
	case "I", "E", "PE", "INPUT", "INPUTS":
		return AreaI, nil
	case "Q", "A", "PA", "OUTPUT", "OUTPUTS":
		return AreaQ, nil
	case "M", "MK", "MARKER", "MARKERS", "FLAGS":
		return AreaM, nil
	case "COIL", "COILS", "C", "0X":
		return AreaCoil, nil
	case "DI", "DISCRETE", "DISCRETE_INPUT", "DISCRETE_INPUTS", "1X":
		return AreaDI, nil
	case "IR", "INPUT_REGISTER", "INPUT_REGISTERS", "3X":
		return AreaIR, nil
	case "HR", "HOLDING", "HOLDING_REGISTER", "HOLDING_REGISTERS", "4X":
		return AreaHR, nil
	case "UA", "OPCUA", "OPC UA", "NODE":
		return AreaUA, nil
	}
	return 0, fmt.Errorf("unknown area %q (ISO-TCP: DB, I, Q, M; Modbus: COIL, DI, IR, HR; OPC UA: UA)", s)
}

// DataType is an elementary data type. Multi-byte values are big-endian, as
// in S7 and in standard Modbus (high word first unless word_order is little).
type DataType int

const (
	TypeBool  DataType = iota
	TypeByte           // 8-bit unsigned (BYTE, USINT)
	TypeSInt           // 8-bit signed
	TypeWord           // 16-bit unsigned (WORD, UINT)
	TypeInt            // 16-bit signed
	TypeDWord          // 32-bit unsigned (DWORD, UDINT)
	TypeDInt           // 32-bit signed
	TypeReal           // IEEE 754 single
	TypeLReal          // IEEE 754 double
)

var typeNames = map[string]DataType{
	"BOOL": TypeBool, "BYTE": TypeByte, "USINT": TypeByte, "SINT": TypeSInt,
	"WORD": TypeWord, "UINT": TypeWord, "INT": TypeInt,
	"DWORD": TypeDWord, "UDINT": TypeDWord, "DINT": TypeDInt,
	"REAL": TypeReal, "LREAL": TypeLReal,
}

func (t DataType) Size() int {
	switch t {
	case TypeBool, TypeByte, TypeSInt:
		return 1
	case TypeWord, TypeInt:
		return 2
	case TypeDWord, TypeDInt, TypeReal:
		return 4
	case TypeLReal:
		return 8
	}
	return 0
}

func (t DataType) IsFloat() bool { return t == TypeReal || t == TypeLReal }

// Tag is one configured value to monitor.
//
// For S7, Offset is a byte offset and Bit is 0..7. For Modbus, Offset is the
// 0-based coil or register number as sent on the wire (holding register
// 40001 is HR offset 0), and Bit is 0..15 within a register.
type Tag struct {
	Name        string  `yaml:"name" json:"name"`
	Area        string  `yaml:"area" json:"area"`
	DB          int     `yaml:"db,omitempty" json:"db"`
	Offset      int     `yaml:"offset" json:"offset"`
	Bit         int     `yaml:"bit,omitempty" json:"bit"`
	Type        string  `yaml:"type" json:"type"`
	Deadband    float64 `yaml:"deadband,omitempty" json:"deadband"`
	Description string  `yaml:"description,omitempty" json:"description"`
	// Unit is the Modbus slave / unit ID for this tag, so one RS485 bus or
	// gateway can serve several devices. 0 uses the network's unit ID.
	Unit int `yaml:"unit,omitempty" json:"unit"`
	// Node is the OPC UA node ID of a UA tag, e.g. ns=3;s="DB1"."Speed".
	Node string `yaml:"node,omitempty" json:"node,omitempty"`

	area      Area
	dtype     DataType
	swapWords bool   // Modbus devices with low word first
	inactive  string // why the tag is not polled (wrong protocol), or ""
	uaOff     int    // UA tags: the tag's slot in the UA image (see Config.validate)
}

func (t *Tag) normalize(protocol string) error {
	if t.Name == "" {
		return fmt.Errorf("name is required")
	}
	a, err := parseArea(t.Area)
	if err != nil {
		return err
	}
	// A tag of the other protocol is kept but not polled, so the network and
	// tag files can be switched to another protocol in either order.
	t.inactive = ""
	if p := a.Protocol(); p != protocol {
		t.inactive = fmt.Sprintf("%s area, but the network protocol is %s", protoName(p), protoName(protocol))
	}
	t.area, t.Area = a, a.String()
	dt, ok := typeNames[strings.ToUpper(strings.TrimSpace(t.Type))]
	if !ok {
		return fmt.Errorf("unknown type %q", t.Type)
	}
	t.dtype, t.Type = dt, strings.ToUpper(strings.TrimSpace(t.Type))
	if a == AreaDB && t.DB <= 0 {
		return fmt.Errorf("area DB needs db >= 1")
	}
	if a != AreaDB {
		t.DB = 0
	}
	if a == AreaUA {
		t.Node = strings.TrimSpace(t.Node)
		if t.Node == "" {
			return fmt.Errorf("an OPC UA tag needs a node ID, e.g. ns=3;s=\"DB1\".\"Speed\"")
		}
		if _, err := ua.ParseNodeID(t.Node); err != nil {
			return fmt.Errorf("node ID %q: %v", t.Node, err)
		}
		t.Offset, t.Bit = 0, 0
	} else {
		t.Node = ""
	}
	if a.Protocol() != ProtoModbus {
		t.Unit = 0
	} else if t.Unit < 0 || t.Unit > 255 {
		return fmt.Errorf("slave ID must be 0..255 (0 = the network's unit ID)")
	}
	if t.Offset < 0 {
		return fmt.Errorf("offset must be >= 0")
	}
	maxBit := 7
	switch {
	case a.isBitArea():
		if dt != TypeBool {
			return fmt.Errorf("%s holds single bits; type must be BOOL", a)
		}
		maxBit = 0
	case a.isRegister():
		if dt.Size() == 1 && dt != TypeBool {
			return fmt.Errorf("%s registers are 16-bit; use BOOL, WORD/UINT, INT or a 32/64-bit type", a)
		}
		maxBit = 15
	}
	if a.Protocol() == ProtoModbus {
		_, end := t.imageRange()
		if (a.isRegister() && end/2 > 65536) || (a.isBitArea() && end > 65536) {
			return fmt.Errorf("offset beyond the Modbus address range (0..65535)")
		}
	}
	if dt == TypeBool && (t.Bit < 0 || t.Bit > maxBit) {
		return fmt.Errorf("bit must be 0..%d", maxBit)
	}
	if dt != TypeBool {
		t.Bit = 0
	}
	if t.Deadband < 0 {
		return fmt.Errorf("deadband must be >= 0")
	}
	return nil
}

func protoName(p string) string {
	switch p {
	case ProtoModbus:
		return "Modbus"
	case ProtoOPCUA:
		return "OPC UA"
	}
	return "ISO-TCP"
}

// Address renders the tag address: Siemens notation for S7 (DB1.DBX0.3,
// MW10, I0.1) and area + 0-based number for Modbus (HR10, HR10.3, C5),
// prefixed with the slave ID when the tag has its own (U3:HR10).
func (t *Tag) Address() string {
	if t.area == AreaUA {
		return t.Node
	}
	if t.area.Protocol() == ProtoModbus {
		unit := ""
		if t.Unit > 0 {
			unit = fmt.Sprintf("U%d:", t.Unit)
		}
		if t.area.isRegister() && t.dtype == TypeBool {
			return fmt.Sprintf("%s%s%d.%d", unit, t.area, t.Offset, t.Bit)
		}
		p := t.area.String()
		if t.area == AreaCoil {
			p = "C"
		}
		return fmt.Sprintf("%s%s%d", unit, p, t.Offset)
	}
	if t.area == AreaDB {
		db := "DB" + strconv.Itoa(t.DB) + "."
		switch t.dtype.Size() {
		case 1:
			if t.dtype == TypeBool {
				return fmt.Sprintf("%sDBX%d.%d", db, t.Offset, t.Bit)
			}
			return fmt.Sprintf("%sDBB%d", db, t.Offset)
		case 2:
			return fmt.Sprintf("%sDBW%d", db, t.Offset)
		case 4:
			return fmt.Sprintf("%sDBD%d", db, t.Offset)
		default:
			return fmt.Sprintf("%sDBX%d.0 (LREAL)", db, t.Offset)
		}
	}
	p := t.area.String()
	switch t.dtype.Size() {
	case 1:
		if t.dtype == TypeBool {
			return fmt.Sprintf("%s%d.%d", p, t.Offset, t.Bit)
		}
		return fmt.Sprintf("%sB%d", p, t.Offset)
	case 2:
		return fmt.Sprintf("%sW%d", p, t.Offset)
	case 4:
		return fmt.Sprintf("%sD%d", p, t.Offset)
	default:
		return fmt.Sprintf("%s%d.0 (LREAL)", p, t.Offset)
	}
}

// Every area is handled as a byte image: S7 areas byte for byte, Modbus
// registers as two big-endian bytes each (register n = bytes 2n, 2n+1), and
// Modbus coils / discrete inputs as one byte (0 or 1) each.

// imageRange is the byte range [start, end) of the image the tag occupies.
func (t *Tag) imageRange() (start, end int) {
	switch {
	case t.area == AreaUA:
		return t.uaOff, t.uaOff + max(1, t.dtype.Size())
	case t.area.isBitArea():
		return t.Offset, t.Offset + 1
	case t.area.isRegister():
		return 2 * t.Offset, 2*t.Offset + max(2, t.dtype.Size())
	}
	return t.Offset, t.Offset + t.dtype.Size()
}

// pos is the byte holding the value (or the bit) and the bit within it.
func (t *Tag) pos() (off, bit int) {
	switch {
	case t.area == AreaUA:
		return t.uaOff, 0
	case t.area.isBitArea():
		return t.Offset, 0
	case t.area.isRegister():
		if t.dtype == TypeBool {
			if t.Bit < 8 {
				return 2*t.Offset + 1, t.Bit // low byte of the register
			}
			return 2 * t.Offset, t.Bit - 8
		}
		return 2 * t.Offset, 0
	}
	return t.Offset, t.Bit
}

// swap reverses the 16-bit word order of a 32/64-bit value in place.
func swapWords(b []byte) {
	for i, j := 0, len(b)-2; i < j; i, j = i+2, j-2 {
		b[i], b[i+1], b[j], b[j+1] = b[j], b[j+1], b[i], b[i+1]
	}
}

// Decode reads the tag value from buf, where buf[0] is image byte base.
// Booleans decode to bool, integers to int64 and floats to float64.
func (t *Tag) Decode(buf []byte, base int) (any, error) {
	off, bit := t.pos()
	o := off - base
	n := t.dtype.Size()
	if o < 0 || o+n > len(buf) {
		return nil, fmt.Errorf("tag %s outside read block", t.Name)
	}
	b := buf[o : o+n]
	if t.swapWords && n >= 4 {
		b = append([]byte(nil), b...)
		swapWords(b)
	}
	switch t.dtype {
	case TypeBool:
		if t.area.isBitArea() {
			return b[0] != 0, nil
		}
		return b[0]&(1<<uint(bit)) != 0, nil
	case TypeByte:
		return int64(b[0]), nil
	case TypeSInt:
		return int64(int8(b[0])), nil
	case TypeWord:
		return int64(binary.BigEndian.Uint16(b)), nil
	case TypeInt:
		return int64(int16(binary.BigEndian.Uint16(b))), nil
	case TypeDWord:
		return int64(binary.BigEndian.Uint32(b)), nil
	case TypeDInt:
		return int64(int32(binary.BigEndian.Uint32(b))), nil
	case TypeReal:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
	case TypeLReal:
		return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
	}
	return nil, fmt.Errorf("unsupported type")
}

// Encode writes v into the image buf at the tag's position (used by the
// simulator and tests).
func (t *Tag) Encode(buf []byte, v any) {
	off, bit := t.pos()
	b := buf[off : off+t.dtype.Size()]
	switch t.dtype {
	case TypeBool:
		on := v.(bool)
		switch {
		case t.area.isBitArea():
			b[0] = 0
			if on {
				b[0] = 1
			}
		case on:
			b[0] |= 1 << uint(bit)
		default:
			b[0] &^= 1 << uint(bit)
		}
	case TypeByte, TypeSInt:
		b[0] = byte(toInt(v))
	case TypeWord, TypeInt:
		binary.BigEndian.PutUint16(b, uint16(toInt(v)))
	case TypeDWord, TypeDInt:
		binary.BigEndian.PutUint32(b, uint32(toInt(v)))
	case TypeReal:
		binary.BigEndian.PutUint32(b, math.Float32bits(float32(toFloat(v))))
	case TypeLReal:
		binary.BigEndian.PutUint64(b, math.Float64bits(toFloat(v)))
	}
	if t.swapWords && len(b) >= 4 {
		swapWords(b)
	}
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// Changed reports whether new differs from old, honouring the tag deadband.
func (t *Tag) Changed(old, new any) bool {
	if old == nil || new == nil {
		return old != new
	}
	if a, ok := old.(float64); ok && math.IsNaN(a) {
		b, _ := new.(float64)
		return !math.IsNaN(b)
	}
	if t.Deadband > 0 && t.dtype != TypeBool {
		return math.Abs(toFloat(new)-toFloat(old)) >= t.Deadband
	}
	return old != new
}

// Writable reports whether the tag's area can be written. Modbus input
// registers and discrete inputs are read-only by definition.
func (t *Tag) Writable() bool {
	return t.inactive == "" && t.area != AreaIR && t.area != AreaDI
}

// Range returns the smallest and largest value the type can hold.
func (t DataType) Range() (lo, hi float64) {
	switch t {
	case TypeBool:
		return 0, 1
	case TypeByte:
		return 0, math.MaxUint8
	case TypeSInt:
		return math.MinInt8, math.MaxInt8
	case TypeWord:
		return 0, math.MaxUint16
	case TypeInt:
		return math.MinInt16, math.MaxInt16
	case TypeDWord:
		return 0, math.MaxUint32
	case TypeDInt:
		return math.MinInt32, math.MaxInt32
	case TypeReal:
		return -math.MaxFloat32, math.MaxFloat32
	}
	return -math.MaxFloat64, math.MaxFloat64
}

// ParseValue converts user input (e.g. "true", "1", "-12", "3.5") to a value
// for this tag, checking that it fits the type.
func (t *Tag) ParseValue(s string) (any, error) {
	s = strings.TrimSpace(s)
	if t.dtype == TypeBool {
		switch strings.ToLower(s) {
		case "1", "true", "on":
			return true, nil
		case "0", "false", "off":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not a BOOL value (use TRUE/FALSE or 1/0)", s)
	}
	lo, hi := t.dtype.Range()
	if t.dtype.IsFloat() {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		if f < lo || f > hi {
			return nil, fmt.Errorf("%g is outside the %s range", f, t.Type)
		}
		return f, nil
	}
	n, err := strconv.ParseInt(s, 0, 64) // also accepts 0x1F, 0b101
	if err != nil {
		return nil, fmt.Errorf("%q is not a whole number", s)
	}
	if float64(n) < lo || float64(n) > hi {
		return nil, fmt.Errorf("%d is outside the %s range (%.0f..%.0f)", n, t.Type, lo, hi)
	}
	return n, nil
}

// FormatValue renders a decoded value for humans.
func FormatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case float64:
		return strconv.FormatFloat(x, 'g', 7, 64)
	}
	return fmt.Sprint(v)
}

// Block is one contiguous image byte range read in a single request.
// gos7 splits large S7 reads into PDU-sized chunks itself; Modbus blocks are
// kept within the protocol limits (125 registers / 2000 coils).
type Block struct {
	Area  Area
	DB    int
	Unit  int // Modbus slave ID; 0 = the network's unit ID
	Start int
	Size  int
	Tags  []*Tag
	// TagErrs, if set by the last read, holds an error per tag (OPC UA reads
	// each node separately; one missing node does not fail the others).
	TagErrs []error
}

// blockLimits returns the largest block and the largest unused gap (both in
// image bytes) for an area. Modbus devices often reject reads of unmapped
// addresses, so by default Modbus blocks only merge adjacent tags.
func blockLimits(a Area, modbusGap int) (size, gap int) {
	switch {
	case a == AreaUA: // 100 nodes per OPC UA Read
		return 100 * 8, 1 << 30
	case a.isRegister():
		return 125 * 2, modbusGap * 2
	case a.isBitArea():
		return 2000, modbusGap
	}
	return 1024, 64
}

// BuildBlocks groups tags into as few reads as reasonable. modbusGap is the
// number of unused registers/coils that may be read to join two blocks.
func BuildBlocks(tags []Tag, modbusGap int) []*Block {
	type key struct {
		unit int
		a    Area
		db   int
	}
	groups := map[key][]*Tag{}
	var keys []key
	for i := range tags {
		t := &tags[i]
		if t.inactive != "" {
			continue
		}
		k := key{t.Unit, t.area, t.DB}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], t)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].unit != keys[j].unit {
			return keys[i].unit < keys[j].unit
		}
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].db < keys[j].db
	})

	var blocks []*Block
	for _, k := range keys {
		maxSize, maxGap := blockLimits(k.a, modbusGap)
		ts := groups[k]
		sort.SliceStable(ts, func(i, j int) bool {
			a, _ := ts[i].imageRange()
			b, _ := ts[j].imageRange()
			return a < b
		})
		var cur *Block
		for _, t := range ts {
			start, end := t.imageRange()
			if cur != nil && start <= cur.Start+cur.Size+maxGap && end-cur.Start <= maxSize {
				if end-cur.Start > cur.Size {
					cur.Size = end - cur.Start
				}
				cur.Tags = append(cur.Tags, t)
				continue
			}
			cur = &Block{Area: k.a, DB: k.db, Unit: k.unit, Start: start, Size: end - start, Tags: []*Tag{t}}
			blocks = append(blocks, cur)
		}
	}
	return blocks
}
