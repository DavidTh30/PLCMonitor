package main

// Reading Excel 97-2003 workbooks (.xls): the compound file (OLE2) around
// the "Workbook" stream, and the BIFF8 records of its first worksheet. Only
// cell values are read (text, numbers, booleans, formula results) – enough
// for tag lists such as HMIEditERP's.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"
)

// cfbStream reads the stream called one of names from a compound file.
func cfbStream(data []byte, names ...string) ([]byte, error) {
	const endChain = 0xFFFFFFFE
	bad := errors.New("not an Excel 97-2003 file (.xls)")
	if len(data) < 512 || !bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) {
		return nil, bad
	}
	le := binary.LittleEndian
	shift := le.Uint16(data[30:])
	if shift != 9 && shift != 12 {
		return nil, bad
	}
	ss := 1 << shift
	mss := 1 << le.Uint16(data[32:])
	cutoff := le.Uint32(data[56:])
	sector := func(n uint32) []byte {
		at := (int(n) + 1) * ss
		if n >= endChain-5 || at < 0 || at+ss > len(data) {
			return nil
		}
		return data[at : at+ss]
	}
	// The FAT: its sectors are listed in the header and the DIFAT chain.
	var fatSecs []uint32
	for i := 0; i < 109; i++ {
		fatSecs = append(fatSecs, le.Uint32(data[76+4*i:]))
	}
	for d, n := le.Uint32(data[68:]), 0; d < endChain-5 && n < 1000; n++ {
		s := sector(d)
		if s == nil {
			return nil, bad
		}
		for i := 0; i < ss/4-1; i++ {
			fatSecs = append(fatSecs, le.Uint32(s[4*i:]))
		}
		d = le.Uint32(s[ss-4:])
	}
	nFat := int(le.Uint32(data[44:]))
	if nFat > len(fatSecs) {
		return nil, bad
	}
	var fat []uint32
	for _, f := range fatSecs[:nFat] {
		s := sector(f)
		if s == nil {
			return nil, bad
		}
		for i := 0; i < ss/4; i++ {
			fat = append(fat, le.Uint32(s[4*i:]))
		}
	}
	chain := func(n uint32, limit int) []byte {
		var out []byte
		for steps := 0; n < endChain-5 && steps <= len(fat) && len(out) < limit+ss; steps++ {
			s := sector(n)
			if s == nil || int(n) >= len(fat) {
				break
			}
			out = append(out, s...)
			n = fat[n]
		}
		return out
	}
	dir := chain(le.Uint32(data[48:]), 1<<24)
	type entry struct {
		name  string
		start uint32
		size  int
	}
	var ents []entry
	for i := 0; i+128 <= len(dir); i += 128 {
		e := dir[i : i+128]
		nl := int(le.Uint16(e[64:]))
		if nl < 2 || nl > 64 {
			ents = append(ents, entry{})
			continue
		}
		u := make([]uint16, nl/2-1)
		for j := range u {
			u[j] = le.Uint16(e[2*j:])
		}
		ents = append(ents, entry{string(utf16.Decode(u)), le.Uint32(e[116:]), int(le.Uint32(e[120:]))})
	}
	if len(ents) == 0 {
		return nil, bad
	}
	for _, want := range names {
		for _, e := range ents[1:] {
			if e.name != want {
				continue
			}
			if e.size >= int(cutoff) {
				out := chain(e.start, e.size)
				if len(out) < e.size {
					return nil, bad
				}
				return out[:e.size], nil
			}
			// A small stream: in the mini stream, chained by the mini FAT.
			mini := chain(ents[0].start, ents[0].size)
			mfRaw := chain(le.Uint32(data[60:]), 1<<24)
			var out []byte
			for n, steps := e.start, 0; n < endChain-5 && steps < len(mfRaw)/4+1; steps++ {
				at := int(n) * mss
				if at+mss > len(mini) || int(n)*4+4 > len(mfRaw) {
					break
				}
				out = append(out, mini[at:at+mss]...)
				n = le.Uint32(mfRaw[4*n:])
			}
			if len(out) < e.size {
				return nil, bad
			}
			return out[:e.size], nil
		}
	}
	return nil, errors.New("the .xls file has no workbook (is it an Excel 97-2003 file?)")
}

// biffReader reads across a record and its CONTINUE records. A string's
// characters that go on in a CONTINUE start with a new option byte there.
type biffReader struct {
	segs [][]byte
	cur  int
	pos  int
}

func (r *biffReader) next() bool {
	if r.cur+1 >= len(r.segs) {
		return false
	}
	r.cur++
	r.pos = 0
	return true
}

func (r *biffReader) bytes(n int) ([]byte, bool) {
	var out []byte
	for n > 0 {
		s := r.segs[r.cur]
		if r.pos >= len(s) {
			if !r.next() {
				return out, false
			}
			continue
		}
		k := min(n, len(s)-r.pos)
		out = append(out, s[r.pos:r.pos+k]...)
		r.pos += k
		n -= k
	}
	return out, true
}

func (r *biffReader) u8() (byte, bool) {
	b, ok := r.bytes(1)
	if !ok {
		return 0, false
	}
	return b[0], true
}

func (r *biffReader) u16() (uint16, bool) {
	b, ok := r.bytes(2)
	if !ok {
		return 0, false
	}
	return binary.LittleEndian.Uint16(b), true
}

// str reads an XLUnicodeRichExtendedString with cch characters.
func (r *biffReader) str(cch int) (string, bool) {
	flags, ok := r.u8()
	if !ok {
		return "", false
	}
	runs, ext := 0, 0
	if flags&8 != 0 {
		v, ok := r.u16()
		if !ok {
			return "", false
		}
		runs = int(v)
	}
	if flags&4 != 0 {
		b, ok := r.bytes(4)
		if !ok {
			return "", false
		}
		ext = int(binary.LittleEndian.Uint32(b))
	}
	u := make([]uint16, 0, cch)
	wide := flags&1 != 0
	for len(u) < cch {
		s := r.segs[r.cur]
		if r.pos >= len(s) { // the characters go on in a CONTINUE
			if !r.next() {
				return "", false
			}
			f, ok := r.u8()
			if !ok {
				return "", false
			}
			wide = f&1 != 0
			continue
		}
		if wide {
			b, ok := r.bytes(2)
			if !ok {
				return "", false
			}
			u = append(u, binary.LittleEndian.Uint16(b))
		} else {
			u = append(u, uint16(s[r.pos]))
			r.pos++
		}
	}
	if _, ok := r.bytes(4*runs + ext); !ok && runs+ext > 0 {
		return "", false
	}
	return string(utf16.Decode(u)), true
}

// xlsNumber shows a cell number as Excel would in General format.
func xlsNumber(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func xlsRK(rk uint32) float64 {
	var v float64
	if rk&2 != 0 {
		v = float64(int32(rk) >> 2)
	} else {
		v = math.Float64frombits(uint64(rk&0xFFFFFFFC) << 32)
	}
	if rk&1 != 0 {
		v /= 100
	}
	return v
}

// xlsRows reads the first worksheet of an .xls file as rows of text.
func xlsRows(data []byte) ([][]string, error) {
	wb, err := cfbStream(data, "Workbook", "Book")
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	type rec struct {
		id   uint16
		body []byte
	}
	var recs []rec
	for p := 0; p+4 <= len(wb); {
		id, n := le.Uint16(wb[p:]), int(le.Uint16(wb[p+2:]))
		if p+4+n > len(wb) {
			break
		}
		recs = append(recs, rec{id, wb[p+4 : p+4+n]})
		p += 4 + n
	}
	if len(recs) == 0 || recs[0].id != 0x0809 || len(recs[0].body) < 4 || le.Uint16(recs[0].body) != 0x0600 {
		return nil, errors.New("only Excel 97-2003 workbooks (BIFF8) can be read; save the file as .xlsx or Excel 97-2003")
	}
	var sst []string
	cells := map[[2]int]string{}
	maxR, maxC := -1, -1
	set := func(r, c int, v string) {
		if r > 65535 || c > 255 {
			return
		}
		cells[[2]int{r, c}] = v
		maxR, maxC = max(maxR, r), max(maxC, c)
	}
	inSheet, done := false, false
	var fr, fc = -1, -1 // a formula whose text result follows in a STRING record
	for i := 0; i < len(recs) && !done; i++ {
		b := recs[i].body
		switch recs[i].id {
		case 0x00FC: // SST
			segs := [][]byte{b}
			for i+1 < len(recs) && recs[i+1].id == 0x003C {
				i++
				segs = append(segs, recs[i].body)
			}
			r := &biffReader{segs: segs}
			head, ok := r.bytes(8)
			if !ok {
				return nil, errors.New("damaged .xls: shared strings")
			}
			for n := int(le.Uint32(head[4:])); n > 0; n-- {
				cch, ok := r.u16()
				if !ok {
					break
				}
				s, ok := r.str(int(cch))
				if !ok {
					break
				}
				sst = append(sst, s)
			}
		case 0x0809: // BOF
			if len(b) >= 4 && le.Uint16(b[2:]) == 0x0010 && !inSheet {
				inSheet = true
			}
		case 0x000A: // EOF
			if inSheet {
				done = true
			}
		}
		if inSheet && recs[i].id == 0x0207 && fr >= 0 && len(b) >= 3 { // STRING after a FORMULA
			rd := &biffReader{segs: [][]byte{b[2:]}}
			if s, ok := rd.str(int(le.Uint16(b))); ok {
				set(fr, fc, s)
			}
			fr = -1
			continue
		}
		if !inSheet || len(b) < 6 {
			continue
		}
		r, c := int(le.Uint16(b)), int(le.Uint16(b[2:]))
		switch recs[i].id {
		case 0x00FD: // LABELSST
			if len(b) >= 10 {
				if k := int(le.Uint32(b[6:])); k < len(sst) {
					set(r, c, sst[k])
				}
			}
		case 0x0204: // LABEL
			if len(b) >= 9 {
				rd := &biffReader{segs: [][]byte{b[8:]}}
				if s, ok := rd.str(int(le.Uint16(b[6:]))); ok {
					set(r, c, s)
				}
			}
		case 0x0203: // NUMBER
			if len(b) >= 14 {
				set(r, c, xlsNumber(math.Float64frombits(le.Uint64(b[6:]))))
			}
		case 0x027E: // RK
			if len(b) >= 10 {
				set(r, c, xlsNumber(xlsRK(le.Uint32(b[6:]))))
			}
		case 0x00BD: // MULRK
			for j, p := 0, 4; p+6 <= len(b)-2; j, p = j+1, p+6 {
				set(r, c+j, xlsNumber(xlsRK(le.Uint32(b[p+2:]))))
			}
		case 0x0205: // BOOLERR
			if len(b) >= 8 && b[7] == 0 {
				set(r, c, map[bool]string{true: "TRUE", false: "FALSE"}[b[6] != 0])
			}
		case 0x0006: // FORMULA: the cached result
			if len(b) >= 14 {
				if le.Uint16(b[12:]) != 0xFFFF {
					set(r, c, xlsNumber(math.Float64frombits(le.Uint64(b[6:]))))
				} else if b[6] == 0 {
					fr, fc = r, c
				} else if b[6] == 1 {
					set(r, c, map[bool]string{true: "TRUE", false: "FALSE"}[b[8] != 0])
				}
			}
		}
	}
	if maxR < 0 {
		return nil, fmt.Errorf("the first worksheet of the .xls file is empty")
	}
	rows := make([][]string, maxR+1)
	for i := range rows {
		rows[i] = make([]string, maxC+1)
	}
	for k, v := range cells {
		rows[k[0]][k[1]] = v
	}
	return rows, nil
}
