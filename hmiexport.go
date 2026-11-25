package main

// Export of the tag list for HMIEditERP: an Excel 97-2003 workbook (.xls)
// with the sheet "DSO_DX" and the columns Name, Type, Address, ByteOrder,
// Extend1, Extend2, as HMIEditERP exports and imports its tag list:
//
//	aa      B  MB0_B             hh      X  Q0.0         cc  W  IW0
//	ff      D  MD0_D             LED1    X  DBxDB100000.0  (DB10.DBX0.0)
//	Word01  W  DBxDBW100001      (DB10.DBW1)
//	Flow    D  DBxDBD100004_D    (DB10.DBD4, also Real)
//
// A DB address is DBxDB, the width, the DB number in 2 digits and the byte
// offset in 4. Type is the width: X bit, B byte, W word, D double word
// (Real too). Only ISO-TCP tags (I, Q, M, DB) fit; Modbus, OPC UA and LReal
// tags are left out.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
)

// hmiAddress is a tag's HMIEditERP type letter and address.
func hmiAddress(t Tag) (typ, addr, reason string) {
	area := strings.ToUpper(t.Area)
	pre := map[string]string{"I": "I", "Q": "Q", "M": "M", "DB": "DB"}[area]
	if pre == "" {
		return "", "", "not an ISO-TCP address (" + t.Area + ")"
	}
	dt, ok := typeNames[strings.ToUpper(t.Type)]
	if !ok {
		return "", "", "type " + t.Type + " unknown"
	}
	if area == "DB" {
		// HMIEditERP: DBxDB + width + DB number (2 digits) + offset (4 digits):
		// DB10.DBX1.2 → DBxDB100001.2, DB10.DBW1 → DBxDBW100001,
		// DB10.DBD1 → DBxDBD100001_D, DB10.DBB1 → DBxDBB100001_B.
		if t.DB < 1 {
			return "", "", "no DB number"
		}
		if t.Offset > 9999 {
			return "", "", "byte offset above 9999 does not fit HMIEditERP's DB address"
		}
		nd := fmt.Sprintf("%02d%04d", t.DB, t.Offset)
		switch {
		case dt == TypeBool:
			return "X", fmt.Sprintf("DBxDB%s.%d", nd, t.Bit), ""
		case dt.Size() == 1:
			return "B", "DBxDBB" + nd + "_B", ""
		case dt.Size() == 2:
			return "W", "DBxDBW" + nd, ""
		case dt.Size() == 4:
			return "D", "DBxDBD" + nd + "_D", ""
		}
		return "", "", "type " + t.Type + " (8 bytes) has no HMIEditERP type"
	}
	switch {
	case dt == TypeBool:
		return "X", fmt.Sprintf("%s%d.%d", pre, t.Offset, t.Bit), ""
	case dt.Size() == 1:
		return "B", fmt.Sprintf("%sB%d_B", pre, t.Offset), ""
	case dt.Size() == 2:
		return "W", fmt.Sprintf("%sW%d", pre, t.Offset), ""
	case dt.Size() == 4:
		return "D", fmt.Sprintf("%sD%d_D", pre, t.Offset), ""
	}
	return "", "", "type " + t.Type + " (8 bytes) has no HMIEditERP type"
}

// HMIExport writes the tags as an HMIEditERP tag list (.xls).
func HMIExport(tags []Tag) (data []byte, rows int, skipped []TIASkipped, err error) {
	table := [][]any{{"Name", "Type", "Address", "ByteOrder", "Extend1", "Extend2"}}
	for _, t := range tags {
		typ, addr, why := hmiAddress(t)
		if why != "" {
			skipped = append(skipped, TIASkipped{t.Name, why})
			continue
		}
		table = append(table, []any{t.Name, typ, addr, 0.0, 0.0, 0.0})
	}
	if len(table) == 1 {
		return nil, 0, skipped, errors.New("no ISO-TCP tags (I, Q, M or DB) to export for the HMI")
	}
	return xlsWorkbook("DSO_DX", table), len(table) - 1, skipped, nil
}

// ---- Excel 97-2003 (.xls, BIFF8) ----

// biff appends one record.
func biff(b *bytes.Buffer, id uint16, body []byte) {
	binary.Write(b, binary.LittleEndian, [2]uint16{id, uint16(len(body))})
	b.Write(body)
}

// xlsString is an XLUnicodeString (16-bit length) or, with short, a
// ShortXLUnicodeString (8-bit length): Latin-1 text stored compressed,
// anything else as UTF-16.
func xlsString(s string, short bool) []byte {
	var b bytes.Buffer
	u := utf16.Encode([]rune(s))
	if short {
		if len(u) > 255 {
			u = u[:255]
		}
		b.WriteByte(byte(len(u)))
	} else {
		if len(u) > 8000 {
			u = u[:8000]
		}
		binary.Write(&b, binary.LittleEndian, uint16(len(u)))
	}
	wide := false
	for _, c := range u {
		wide = wide || c > 0xFF
	}
	if !wide {
		b.WriteByte(0)
		for _, c := range u {
			b.WriteByte(byte(c))
		}
	} else {
		b.WriteByte(1)
		binary.Write(&b, binary.LittleEndian, u)
	}
	return b.Bytes()
}

// xlsWorkbook makes a workbook with one sheet; cells are strings or float64.
func xlsWorkbook(sheet string, table [][]any) []byte {
	le := binary.LittleEndian
	u16 := func(v ...uint16) []byte {
		b := make([]byte, 2*len(v))
		for i, x := range v {
			le.PutUint16(b[2*i:], x)
		}
		return b
	}
	bof := func(kind uint16) []byte { return append(u16(0x0600, kind, 0x0DBB, 0x07CC), 0, 0, 0, 0, 6, 0, 0, 0) }

	// The sheet first, to know its size; the workbook globals point at it.
	var sh bytes.Buffer
	biff(&sh, 0x0809, bof(0x0010))
	cols := 0
	for _, r := range table {
		cols = max(cols, len(r))
	}
	dim := make([]byte, 14)
	le.PutUint32(dim[4:], uint32(len(table)))
	le.PutUint16(dim[10:], uint16(cols))
	biff(&sh, 0x0200, dim)                                    // DIMENSIONS
	biff(&sh, 0x023E, u16(0x06B6, 0, 0, 0x40, 0, 0, 0, 0, 0)) // WINDOW2
	for i, r := range table {
		for j, v := range r {
			head := u16(uint16(i), uint16(j), 15)
			switch x := v.(type) {
			case string:
				biff(&sh, 0x0204, append(head, xlsString(x, false)...)) // LABEL
			case float64:
				num := make([]byte, 8)
				le.PutUint64(num, math.Float64bits(x))
				biff(&sh, 0x0203, append(head, num...)) // NUMBER
			}
		}
	}
	biff(&sh, 0x000A, nil) // EOF

	var g bytes.Buffer
	biff(&g, 0x0809, bof(0x0005))
	biff(&g, 0x0042, u16(1200))                                       // CODEPAGE: UTF-16
	biff(&g, 0x003D, u16(0, 0, 0x4000, 0x2000, 0x38, 0, 0, 1, 0x258)) // WINDOW1
	font := append(u16(200, 0, 0x7FFF, 400, 0), 0, 0, 0, 0)
	font = append(font, xlsString("Arial", true)...)
	for range 4 {
		biff(&g, 0x0031, font) // FONT (index 4 is never used)
	}
	styleXF := []byte{0, 0, 0, 0, 0xF5, 0xFF, 0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xC0, 0x20}
	cellXF := []byte{0, 0, 0, 0, 0x01, 0x00, 0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xC0, 0x20}
	for range 15 {
		biff(&g, 0x00E0, styleXF) // XF 0..14: cell styles
	}
	biff(&g, 0x00E0, cellXF)                      // XF 15: the cells
	biff(&g, 0x0293, []byte{0x00, 0x80, 0, 0xFF}) // STYLE: Normal
	name := xlsString(sheet, true)
	bs := make([]byte, 6, 6+len(name))
	bsAt := g.Len() + 4                   // where the sheet's offset goes
	biff(&g, 0x0085, append(bs, name...)) // BOUNDSHEET
	biff(&g, 0x000A, nil)
	wb := g.Bytes()
	le.PutUint32(wb[bsAt:], uint32(len(wb)))
	wb = append(wb, sh.Bytes()...)
	return cfbFile("Workbook", wb)
}

// cfbFile wraps one stream in a compound file (OLE2), as .xls files are.
// The stream is padded to 4096 bytes so it lives in normal sectors.
func cfbFile(name string, stream []byte) []byte {
	const (
		sec      = 512
		free     = 0xFFFFFFFF
		endChain = 0xFFFFFFFE
		fatSect  = 0xFFFFFFFD
	)
	if len(stream) < 4096 {
		stream = append(stream, make([]byte, 4096-len(stream))...)
	}
	size := len(stream)
	if size%sec != 0 {
		stream = append(stream, make([]byte, sec-size%sec)...)
	}
	n := len(stream) / sec // stream sectors
	nFat := 1
	for n+nFat+1 > nFat*128 {
		nFat++
	}
	le := binary.LittleEndian
	fat := make([]uint32, nFat*128)
	for i := range fat {
		fat[i] = free
	}
	for i := 0; i < n; i++ {
		fat[i] = uint32(i + 1)
	}
	fat[n-1] = endChain
	for i := 0; i < nFat; i++ {
		fat[n+i] = fatSect
	}
	dirSec := n + nFat
	fat[dirSec] = endChain

	head := make([]byte, sec)
	copy(head, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	le.PutUint16(head[24:], 0x3E)
	le.PutUint16(head[26:], 3)
	le.PutUint16(head[28:], 0xFFFE)
	le.PutUint16(head[30:], 9)
	le.PutUint16(head[32:], 6)
	le.PutUint32(head[44:], uint32(nFat))
	le.PutUint32(head[48:], uint32(dirSec))
	le.PutUint32(head[56:], 4096)
	le.PutUint32(head[60:], endChain)
	le.PutUint32(head[68:], endChain)
	for i := 0; i < 109; i++ {
		v := uint32(free)
		if i < nFat {
			v = uint32(n + i)
		}
		le.PutUint32(head[76+4*i:], v)
	}

	entry := func(name string, typ byte, child, start uint32, size uint32) []byte {
		e := make([]byte, 128)
		u := utf16.Encode([]rune(name))
		for i, c := range u {
			le.PutUint16(e[2*i:], c)
		}
		le.PutUint16(e[64:], uint16(2*len(u)+2))
		e[66], e[67] = typ, 1 // black
		le.PutUint32(e[68:], free)
		le.PutUint32(e[72:], free)
		le.PutUint32(e[76:], child)
		le.PutUint32(e[116:], start)
		le.PutUint32(e[120:], size)
		return e
	}
	dir := make([]byte, 0, sec)
	dir = append(dir, entry("Root Entry", 5, 1, endChain, 0)...)
	dir = append(dir, entry(name, 2, free, 0, uint32(size))...)
	for len(dir) < sec {
		e := make([]byte, 128)
		le.PutUint32(e[68:], free)
		le.PutUint32(e[72:], free)
		le.PutUint32(e[76:], free)
		dir = append(dir, e...)
	}

	var out bytes.Buffer
	out.Write(head)
	out.Write(stream)
	binary.Write(&out, le, fat)
	out.Write(dir)
	return out.Bytes()
}
