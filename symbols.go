package main

// Variable names for Scan PLC. S7 PUT/GET only carries bytes, so the names
// come from files exported from TIA Portal / STEP 7:
//
//   - the source of data blocks and PLC data types ("Generate source from
//     blocks": .db, .udt, .scl; STEP 7 classic .awl). The offsets of the
//     variables are worked out here with the layout rules of standard
//     (non-optimized) block access, the only kind PUT/GET can read;
//   - a PLC tag table (Export → .xlsx, or .csv) for the M, I and Q areas.
//
// The imported files are kept in plc-symbols.json next to the config, with
// the DB number given to each data block (a TIA source has only its name).

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Symbol is one variable with its absolute address.
type Symbol struct {
	Name     string `json:"name"` // path inside the block, e.g. Motor.Speed or Arr[3]
	Area     string `json:"area"` // DB, M, I or Q
	Offset   int    `json:"offset"`
	Bit      int    `json:"bit"`
	Size     int    `json:"size"`           // bytes (0 for BOOL)
	Type     string `json:"type,omitempty"` // tag type; "" if it can't be a tag (String, DTL, …)
	DataType string `json:"data_type"`      // as written in the source
	Comment  string `json:"comment,omitempty"`
}

// SymBlock is one imported data block, or one tag table.
type SymBlock struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"` // db or tags
	DB        int      `json:"db"`   // number given to the data block (0 = not set)
	Size      int      `json:"size"` // bytes, from the layout
	Optimized bool     `json:"optimized,omitempty"`
	File      string   `json:"file"`
	Note      string   `json:"note,omitempty"`
	Symbols   []Symbol `json:"symbols"`
}

const maxSymbols = 20000 // per block; large arrays are cut off

// ---- source lexer ----

type tokKind int

const (
	tIdent tokKind = iota
	tQuoted
	tString
	tNumber
	tSym
	tAttr
)

type token struct {
	kind tokKind
	text string
	line int
}

func (t token) up() string { return strings.ToUpper(t.text) }

// lexSource splits a block source into tokens. Line comments are returned
// separately by line, so a declaration can pick up its comment.
func lexSource(src string) ([]token, map[int]string) {
	var toks []token
	comments := map[int]string{}
	line := 1
	r := []rune(src)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == '\n':
			line++
			i++
		case unicode.IsSpace(c) || c == '\uFEFF':
			i++
		case c == '/' && i+1 < len(r) && r[i+1] == '/':
			j := i + 2
			for j < len(r) && r[j] != '\n' {
				j++
			}
			comments[line] = strings.TrimSpace(string(r[i+2 : j]))
			i = j
		case c == '(' && i+1 < len(r) && r[i+1] == '*':
			j := i + 2
			for j+1 < len(r) && !(r[j] == '*' && r[j+1] == ')') {
				if r[j] == '\n' {
					line++
				}
				j++
			}
			i = j + 2
		case c == '{':
			j, start := i+1, line
			for j < len(r) && r[j] != '}' {
				if r[j] == '\n' {
					line++
				}
				j++
			}
			toks = append(toks, token{tAttr, string(r[i+1 : min(j, len(r))]), start})
			i = j + 1
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(r) && r[j] != c && r[j] != '\n' {
				j++
			}
			k := tQuoted
			if c == '\'' {
				k = tString
			}
			toks = append(toks, token{k, string(r[i+1 : min(j, len(r))]), line})
			i = j + 1
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_') {
				j++
			}
			if j < len(r) && r[j] == '#' { // T#5s, DINT#3, …
				for j < len(r) && !unicode.IsSpace(r[j]) && !strings.ContainsRune(";,)]", r[j]) {
					j++
				}
			}
			toks = append(toks, token{tIdent, string(r[i:j]), line})
			i = j
		case unicode.IsDigit(c):
			j := i
			for j < len(r) && unicode.IsDigit(r[j]) {
				j++
			}
			if j+1 < len(r) && r[j] == '.' && unicode.IsDigit(r[j+1]) {
				j++
				for j < len(r) && (unicode.IsDigit(r[j]) || r[j] == 'e' || r[j] == 'E') {
					j++
				}
			}
			if j < len(r) && r[j] == '#' { // 16#FF
				for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '#' || r[j] == '_') {
					j++
				}
			}
			toks = append(toks, token{tNumber, string(r[i:j]), line})
			i = j
		default:
			s := string(c)
			if i+1 < len(r) && (string(r[i:i+2]) == ".." || string(r[i:i+2]) == ":=") {
				s = string(r[i : i+2])
			}
			toks = append(toks, token{tSym, s, line})
			i += len([]rune(s))
		}
	}
	return toks, comments
}

// ---- source parser ----

type symTypeKind int

const (
	kElem symTypeKind = iota
	kArray
	kStruct
	kRef
	kString
	kFB // a function block (its instance data)
)

type symType struct {
	kind    symTypeKind
	name    string   // elementary type (upper case) or referenced type
	dims    [][2]int // array bounds
	elem    *symType
	members []symMember
	strLen  int
	wide    bool
	orig    string // as written (elementary types)

	// kFB: a function block's instance data, sections in, out, in_out, static.
	sections  [][]symMember
	optimized bool
}

// sysFB describes the instance data of a built-in function block in a
// block with standard access: its variables at fixed places, as bit
// offsets from its start. align is false for the edge detectors, whose
// three bits follow the variable before them without a gap.
type sysFB struct {
	bits  int // size in bits
	align bool
	vars  []sysVar
}

type sysVar struct {
	name string
	bit  int    // bit offset from the start
	typ  string // elementary type
}

// IEC timers (IEC_TIMER, 16 bytes): start time, PT, ET, then the bits
// RU, IN, Q – checked against the instance data of a 1200-series CPU.
var iecTimer = &sysFB{bits: 16 * 8, align: true, vars: []sysVar{
	{"PT", 4 * 8, "TIME"}, {"ET", 8 * 8, "TIME"}, {"IN", 12*8 + 1, "BOOL"}, {"Q", 12*8 + 2, "BOOL"}}}

// IEC counters (IEC_COUNTER with INT values, 6 bytes).
var iecCounter = &sysFB{bits: 6 * 8, align: true, vars: []sysVar{
	{"CU", 0, "BOOL"}, {"CD", 1, "BOOL"}, {"R", 2, "BOOL"}, {"LD", 3, "BOOL"}, {"QU", 4, "BOOL"}, {"QD", 5, "BOOL"},
	{"PV", 2 * 8, "INT"}, {"CV", 4 * 8, "INT"}}}

// Edge detectors: CLK, Q and the edge memory bit.
var edgeFB = &sysFB{bits: 3, vars: []sysVar{{"CLK", 0, "BOOL"}, {"Q", 1, "BOOL"}, {"Stat_Bit", 2, "BOOL"}}}

// sysFBs are the built-in function blocks whose instance layout is known.
var sysFBs = map[string]*sysFB{
	"R_TRIG": edgeFB, "F_TRIG": edgeFB,
	"TON": iecTimer, "TOF": iecTimer, "TP": iecTimer, "TONR": iecTimer,
	"TON_TIME": iecTimer, "TOF_TIME": iecTimer, "TP_TIME": iecTimer, "TONR_TIME": iecTimer, "IEC_TIMER": iecTimer,
	"CTU": iecCounter, "CTD": iecCounter, "CTUD": iecCounter,
	"CTU_INT": iecCounter, "CTD_INT": iecCounter, "CTUD_INT": iecCounter, "IEC_COUNTER": iecCounter,
}

type symMember struct {
	name    string
	typ     *symType
	comment string
}

// dbDef is a DATA_BLOCK found in a source.
type dbDef struct {
	name      string
	number    int // from a STEP 7 classic source (DATA_BLOCK DB 10)
	optimized bool
	typ       *symType // its structure (nil for an instance DB)
	ref       string   // the type or FB it is made from
}

type srcParser struct {
	toks     []token
	comments map[int]string
	i        int
}

func (p *srcParser) peek() token {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return token{tSym, "", 0}
}
func (p *srcParser) next() token { t := p.peek(); p.i++; return t }
func (p *srcParser) eof() bool   { return p.i >= len(p.toks) }

// skipAttrs skips {…} attribute blocks.
func (p *srcParser) skipAttrs() {
	for !p.eof() && p.peek().kind == tAttr {
		p.i++
	}
}

func (p *srcParser) expect(s string) error {
	t := p.next()
	if t.up() != s {
		return fmt.Errorf("line %d: expected %q, found %q", t.line, s, t.text)
	}
	return nil
}

// blockName reads a block name: "Name", Name, DB 10, UDT 5, FB 3.
func (p *srcParser) blockName() (string, int) {
	t := p.next()
	if t.kind == tIdent {
		switch t.up() {
		case "DB", "UDT", "FB", "SFB":
			if p.peek().kind == tNumber {
				n, _ := strconv.Atoi(p.next().text)
				return t.up() + " " + strconv.Itoa(n), n
			}
		}
	}
	return t.text, 0
}

func parseSource(src string) (types map[string]*symType, dbs []dbDef, err error) {
	toks, comments := lexSource(src)
	p := &srcParser{toks: toks, comments: comments}
	types = map[string]*symType{}
	for !p.eof() {
		t := p.next()
		if t.kind != tIdent {
			continue
		}
		switch t.up() {
		case "TYPE":
			name, _ := p.blockName()
			for !p.eof() && p.peek().up() != "STRUCT" && p.peek().up() != "END_TYPE" {
				p.i++
			}
			if p.peek().up() == "STRUCT" {
				p.i++
				st, err := p.members(0)
				if err != nil {
					return nil, nil, fmt.Errorf("type %s: %w", name, err)
				}
				types[strings.ToUpper(name)] = st
			}
			p.skipTo("END_TYPE")
		case "DATA_BLOCK":
			name, num := p.blockName()
			d := dbDef{name: name}
			if strings.HasPrefix(name, "DB ") {
				d.number = num
			}
		header:
			for !p.eof() {
				h := p.peek()
				switch {
				case h.kind == tAttr:
					if regexp.MustCompile(`(?i)S7_Optimized_Access\s*:=\s*'TRUE'`).MatchString(h.text) {
						d.optimized = true
					}
					p.i++
				case h.up() == "STRUCT":
					p.i++
					st, err := p.members(0)
					if err != nil {
						return nil, nil, fmt.Errorf("data block %s: %w", name, err)
					}
					d.typ = st
					break header
				case h.up() == "BEGIN" || h.up() == "END_DATA_BLOCK":
					break header
				case h.kind == tQuoted:
					d.ref = h.text
					p.i++
				case h.kind == tIdent && (h.up() == "FB" || h.up() == "SFB" || h.up() == "UDT"):
					d.ref, _ = p.blockName()
				case h.kind == tIdent && sysFBs[h.up()] != nil: // instance DB of a timer, R_TRIG, …
					d.ref = h.up()
					p.i++
				case h.kind == tIdent && (h.up() == "TITLE" || h.up() == "AUTHOR" || h.up() == "FAMILY" || h.up() == "NAME"):
					// TITLE = some text up to the end of the line (STEP 7 classic)
					l := h.line
					for !p.eof() && p.peek().line == l {
						p.i++
					}
				default:
					p.i++
				}
			}
			dbs = append(dbs, d)
			p.skipTo("END_DATA_BLOCK")
		case "FUNCTION_BLOCK":
			name, _ := p.blockName()
			fb, err := p.fbInterface()
			if err != nil {
				return nil, nil, fmt.Errorf("function block %s: %w", name, err)
			}
			fb.name = name
			types[strings.ToUpper(name)] = fb
			p.skipTo("END_FUNCTION_BLOCK")
		case "FUNCTION", "ORGANIZATION_BLOCK":
			p.skipTo("END_" + t.up())
		}
	}
	return types, dbs, nil
}

// fbInterface reads a function block's declaration sections, up to BEGIN.
// Its instance data holds VAR_INPUT, VAR_OUTPUT, VAR_IN_OUT and VAR (static)
// in that order; VAR_TEMP and VAR CONSTANT take no room.
func (p *srcParser) fbInterface() (*symType, error) {
	fb := &symType{kind: kFB}
	order := map[string]int{"VAR_INPUT": 0, "VAR_OUTPUT": 1, "VAR_IN_OUT": 2, "VAR": 3}
	var secs [4][]symMember
	for !p.eof() {
		h := p.peek()
		switch u := h.up(); {
		case h.kind == tAttr:
			if regexp.MustCompile(`(?i)S7_Optimized_Access\s*:=\s*'TRUE'`).MatchString(h.text) {
				fb.optimized = true
			}
			p.i++
		case u == "BEGIN" || u == "END_FUNCTION_BLOCK":
			fb.sections = secs[:]
			return fb, nil
		case u == "VAR_INPUT" || u == "VAR_OUTPUT" || u == "VAR_IN_OUT" || u == "VAR" || u == "VAR_TEMP" || u == "VAR_STAT":
			p.i++
			keep := u != "VAR_TEMP"
			// VAR RETAIN / NON_RETAIN / DB_SPECIFIC are static; VAR CONSTANT takes no room.
			for q := p.peek().up(); q == "RETAIN" || q == "NON_RETAIN" || q == "DB_SPECIFIC" || q == "CONSTANT"; q = p.peek().up() {
				if q == "CONSTANT" {
					keep = false
				}
				p.i++
			}
			st, err := p.membersUntil(0, "END_VAR")
			if err != nil {
				return nil, fmt.Errorf("%s: %w", u, err)
			}
			if keep {
				k := order[u]
				if u == "VAR_STAT" {
					k = 3
				}
				secs[k] = append(secs[k], st.members...)
			}
		default:
			p.i++
		}
	}
	return nil, errors.New("missing BEGIN")
}

func (p *srcParser) skipTo(kw string) {
	for !p.eof() && p.next().up() != kw {
	}
}

// members parses declarations up to END_STRUCT (which it consumes).
func (p *srcParser) members(depth int) (*symType, error) { return p.membersUntil(depth, "END_STRUCT") }

// membersUntil parses declarations up to end (END_STRUCT, or END_VAR for a
// declaration section of a function block), which it consumes.
func (p *srcParser) membersUntil(depth int, end string) (*symType, error) {
	if depth > 30 {
		return nil, errors.New("structures nested too deep")
	}
	st := &symType{kind: kStruct}
	for {
		p.skipAttrs()
		if p.eof() {
			return nil, errors.New("missing " + end)
		}
		t := p.next()
		if t.up() == end {
			return st, nil
		}
		if t.kind != tIdent && t.kind != tQuoted {
			if t.text == ";" {
				continue
			}
			return nil, fmt.Errorf("line %d: unexpected %q", t.line, t.text)
		}
		p.skipAttrs()
		if err := p.expect(":"); err != nil {
			return nil, err
		}
		p.skipAttrs()
		typ, err := p.typeSpec(depth)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.text, err)
		}
		endLine := t.line
		if p.peek().text == ":=" { // start value
			for !p.eof() && p.peek().text != ";" {
				p.i++
			}
		}
		if p.peek().text == ";" {
			endLine = p.next().line
		}
		c := p.comments[t.line]
		if c == "" {
			c = p.comments[endLine]
		}
		st.members = append(st.members, symMember{name: t.text, typ: typ, comment: c})
	}
}

func (p *srcParser) intLit() (int, error) {
	neg := false
	if p.peek().text == "-" {
		neg = true
		p.i++
	}
	t := p.next()
	n, err := strconv.Atoi(t.text)
	if err != nil {
		return 0, fmt.Errorf("line %d: expected a number, found %q", t.line, t.text)
	}
	if neg {
		n = -n
	}
	return n, nil
}

func (p *srcParser) typeSpec(depth int) (*symType, error) {
	t := p.next()
	if t.kind == tQuoted {
		return &symType{kind: kRef, name: t.text}, nil
	}
	if t.kind != tIdent {
		return nil, fmt.Errorf("line %d: expected a data type, found %q", t.line, t.text)
	}
	switch u := t.up(); u {
	case "STRUCT":
		return p.members(depth + 1)
	case "ARRAY":
		if err := p.expect("["); err != nil {
			return nil, err
		}
		a := &symType{kind: kArray}
		for {
			lo, err := p.intLit()
			if err != nil {
				return nil, err
			}
			if err := p.expect(".."); err != nil {
				return nil, err
			}
			hi, err := p.intLit()
			if err != nil {
				return nil, err
			}
			if hi < lo {
				return nil, fmt.Errorf("line %d: array bounds %d..%d", t.line, lo, hi)
			}
			a.dims = append(a.dims, [2]int{lo, hi})
			if p.peek().text != "," {
				break
			}
			p.i++
		}
		if err := p.expect("]"); err != nil {
			return nil, err
		}
		if err := p.expect("OF"); err != nil {
			return nil, err
		}
		p.skipAttrs()
		el, err := p.typeSpec(depth + 1)
		if err != nil {
			return nil, err
		}
		a.elem = el
		return a, nil
	case "STRING", "WSTRING":
		s := &symType{kind: kString, strLen: 254, wide: u == "WSTRING"}
		if p.peek().text == "[" {
			p.i++
			n, err := p.intLit()
			if err != nil {
				return nil, err
			}
			s.strLen = n
			if err := p.expect("]"); err != nil {
				return nil, err
			}
		}
		return s, nil
	case "UDT":
		if p.peek().kind == tNumber {
			return &symType{kind: kRef, name: "UDT " + p.next().text}, nil
		}
	}
	return &symType{kind: kElem, name: t.up(), orig: t.text}, nil
}

// ---- layout (standard block access, as S7-300/400) ----

// elemSize is the size in bytes of elementary types; BOOL is 0 (one bit).
var elemSize = map[string]int{
	"BOOL": 0, "BYTE": 1, "CHAR": 1, "SINT": 1, "USINT": 1,
	"WORD": 2, "INT": 2, "UINT": 2, "DATE": 2, "S5TIME": 2, "WCHAR": 2,
	"DWORD": 4, "DINT": 4, "UDINT": 4, "REAL": 4, "TIME": 4, "TOD": 4, "TIME_OF_DAY": 4,
	"LREAL": 8, "LWORD": 8, "LINT": 8, "ULINT": 8, "LTIME": 8, "LTOD": 8, "LTIME_OF_DAY": 8,
	"LDT": 8, "DT": 8, "DATE_AND_TIME": 8, "DTL": 12,
}

// tagTypeOf is the tag type a variable is read as.
var tagTypeOf = map[string]string{
	"BOOL": "BOOL", "BYTE": "BYTE", "CHAR": "BYTE", "SINT": "SINT", "USINT": "USINT",
	"WORD": "WORD", "INT": "INT", "UINT": "UINT", "DATE": "UINT", "S5TIME": "WORD", "WCHAR": "WORD",
	"DWORD": "DWORD", "DINT": "DINT", "UDINT": "UDINT", "REAL": "REAL", "TIME": "DINT",
	"TOD": "UDINT", "TIME_OF_DAY": "UDINT", "LREAL": "LREAL",
}

type layouter struct {
	types map[string]*symType
	pos   int // bit position
	out   []Symbol
	cut   bool
	// gap is the bit after the last Bool of the current function block
	// section (-1: none). An edge detector's three bits go there when the
	// byte has room, even if other variables came after the Bool – as the
	// PLC lays it out (checked against a 1200-series CPU).
	gap int
}

func (l *layouter) align(bits int) { l.pos = (l.pos + bits - 1) / bits * bits }

func (l *layouter) emit(s Symbol) {
	if len(l.out) >= maxSymbols {
		l.cut = true
		return
	}
	l.out = append(l.out, s)
}

// joinName is parent.child, or child at the top of a block.
func joinName(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// placeSys lays out the instance of a built-in function block.
func (l *layouter) placeSys(name, typeName string, s *sysFB, comment string) {
	if s.align {
		l.align(16)
	}
	base := l.pos
	if !s.align && l.gap >= 0 && l.gap%8+s.bits <= 8 {
		// into the free bits of the last Bool byte of this section
		base = l.gap
		for _, v := range s.vars {
			p := base + v.bit
			l.emit(Symbol{Name: joinName(name, v.name), Area: "DB", Offset: p / 8, Bit: p % 8, DataType: v.typ, Type: tagTypeOf[v.typ], Comment: comment})
		}
		l.gap = base + s.bits
		l.pos = max(l.pos, l.gap)
		return
	}
	for _, v := range s.vars {
		p := base + v.bit
		sym := Symbol{Name: joinName(name, v.name), Area: "DB", Offset: p / 8, DataType: v.typ, Type: tagTypeOf[v.typ], Size: elemSize[v.typ], Comment: comment}
		if v.typ == "BOOL" {
			sym.Bit = p % 8
		}
		l.emit(sym)
	}
	l.pos = base + s.bits
	if s.align {
		l.align(16)
	} else {
		l.gap = l.pos
	}
}

func typeText(t *symType) string {
	switch t.kind {
	case kFB:
		return `"` + t.name + `"`
	case kElem:
		if t.orig != "" {
			return t.orig
		}
		return t.name
	case kRef:
		return `"` + t.name + `"`
	case kString:
		if t.wide {
			return fmt.Sprintf("WString[%d]", t.strLen)
		}
		return fmt.Sprintf("String[%d]", t.strLen)
	case kStruct:
		return "Struct"
	}
	var b strings.Builder
	b.WriteString("Array[")
	for i, d := range t.dims {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%d..%d", d[0], d[1])
	}
	b.WriteString("] of " + typeText(t.elem))
	return b.String()
}

func (l *layouter) place(name string, t *symType, comment string, depth int) error {
	if depth > 40 {
		return errors.New("types nested too deep (or a type contains itself)")
	}
	switch t.kind {
	case kElem:
		size, ok := elemSize[t.name]
		if !ok {
			if s := sysFBs[t.name]; s != nil {
				l.placeSys(name, t.orig, s, comment)
				return nil
			}
			return fmt.Errorf("%s: data type %s is not supported", name, t.name)
		}
		if t.name == "BOOL" {
			l.emit(Symbol{Name: name, Area: "DB", Offset: l.pos / 8, Bit: l.pos % 8, Type: "BOOL", DataType: typeText(t), Comment: comment})
			l.pos++
			l.gap = l.pos
			return nil
		}
		if size == 1 {
			l.align(8)
		} else {
			l.align(16)
		}
		l.emit(Symbol{Name: name, Area: "DB", Offset: l.pos / 8, Size: size, Type: tagTypeOf[t.name], DataType: typeText(t), Comment: comment})
		l.pos += size * 8
	case kString:
		l.align(16)
		size := t.strLen + 2
		if t.wide {
			size = 2*t.strLen + 4
		}
		l.emit(Symbol{Name: name, Area: "DB", Offset: l.pos / 8, Size: size, DataType: typeText(t), Comment: comment})
		l.pos += size * 8
	case kRef:
		rt := l.types[strings.ToUpper(t.name)]
		if rt == nil {
			if s := sysFBs[strings.ToUpper(t.name)]; s != nil {
				l.placeSys(name, t.name, s, comment)
				return nil
			}
			return fmt.Errorf("%s: data type or function block \"%s\" is unknown – import its source too", name, t.name)
		}
		return l.place(name, rt, comment, depth+1)
	case kFB:
		// Standard access: input, output, in/out and static variables in
		// that order, each section starting on an even byte. An in/out
		// parameter of a structured type is a 6-byte pointer.
		outerGap := l.gap
		defer func() { l.gap = outerGap }()
		for si, sec := range t.sections {
			if len(sec) == 0 {
				continue
			}
			l.align(16)
			l.gap = -1
			for _, m := range sec {
				n := m.name
				if name != "" {
					n = name + "." + m.name
				}
				c := m.comment
				if c == "" {
					c = comment
				}
				if si == 2 && m.typ.kind != kElem {
					l.align(16)
					l.emit(Symbol{Name: n, Area: "DB", Offset: l.pos / 8, Size: 6, DataType: "Pointer (in/out " + typeText(m.typ) + ")", Comment: c})
					l.pos += 6 * 8
					continue
				}
				if err := l.place(n, m.typ, c, depth+1); err != nil {
					return err
				}
			}
		}
		l.align(16)
	case kStruct:
		l.align(16)
		outerGap := l.gap
		defer func() { l.gap = outerGap }()
		for _, m := range t.members {
			n := m.name
			if name != "" {
				n = name + "." + m.name
			}
			c := m.comment
			if c == "" {
				c = comment
			}
			if err := l.place(n, m.typ, c, depth+1); err != nil {
				return err
			}
		}
		l.align(16)
	case kArray:
		l.align(16)
		outerGap := l.gap
		defer func() { l.gap = outerGap }()
		total := 1
		for _, d := range t.dims {
			total *= d[1] - d[0] + 1
			if total > 1<<20 {
				return fmt.Errorf("%s: array too large", name)
			}
		}
		idx := make([]int, len(t.dims))
		for i := range idx {
			idx[i] = t.dims[i][0]
		}
		for n := 0; n < total; n++ {
			parts := make([]string, len(idx))
			for i, v := range idx {
				parts[i] = strconv.Itoa(v)
			}
			if err := l.place(name+"["+strings.Join(parts, ",")+"]", t.elem, comment, depth+1); err != nil {
				return err
			}
			for i := len(idx) - 1; i >= 0; i-- { // last index runs fastest
				if idx[i]++; idx[i] <= t.dims[i][1] {
					break
				}
				idx[i] = t.dims[i][0]
			}
		}
		l.align(16)
	}
	return nil
}

// ---- tag tables ----

var plcAddrRe = regexp.MustCompile(`^%?\s*([IEQAM])\s*([XBWD]?)\s*(\d+)(?:\.(\d))?`)

// tagTableSymbols turns rows (name, data type, address, comment) into symbols.
func tagTableSymbols(rows [][4]string) []Symbol {
	var out []Symbol
	for _, r := range rows {
		m := plcAddrRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(r[2])))
		if m == nil || strings.TrimSpace(r[0]) == "" {
			continue
		}
		area := map[string]string{"I": "I", "E": "I", "Q": "Q", "A": "Q", "M": "M"}[m[1]]
		off, _ := strconv.Atoi(m[3])
		dt := strings.ToUpper(strings.TrimSpace(r[1]))
		s := Symbol{Name: strings.TrimSpace(r[0]), Area: area, Offset: off, DataType: strings.TrimSpace(r[1]), Comment: strings.TrimSpace(r[3])}
		if m[4] != "" { // a bit address
			s.Bit, _ = strconv.Atoi(m[4])
			if s.Bit > 7 {
				continue
			}
			s.Type = "BOOL"
		} else {
			s.Type = tagTypeOf[dt]
			s.Size = elemSize[dt]
			if s.Type == "" || s.Size == 0 { // type unknown: go by the address width
				s.Size = map[string]int{"": 1, "X": 1, "B": 1, "W": 2, "D": 4}[m[2]]
				s.Type = map[int]string{1: "BYTE", 2: "WORD", 4: "DWORD"}[s.Size]
			}
		}
		out = append(out, s)
	}
	return out
}

// tableRows finds the name, data type, address and comment columns by their
// headings (TIA Portal: Name, Data Type, Logical Address, Comment).
func tableRows(rows [][]string) ([][4]string, error) {
	col := [4]int{-1, -1, -1, -1}
	head := -1
	for i, r := range rows {
		for j, c := range r {
			switch strings.ToLower(strings.TrimSpace(c)) {
			case "name", "symbol":
				col[0] = j
			case "data type", "datatype", "data_type", "type":
				col[1] = j
			case "logical address", "address", "logicaladdress":
				col[2] = j
			case "comment":
				col[3] = j
			}
		}
		if col[0] >= 0 && col[2] >= 0 {
			head = i
			break
		}
		col = [4]int{-1, -1, -1, -1}
		if i > 20 {
			break
		}
	}
	if head < 0 {
		return nil, errors.New("no 'Name' and 'Logical Address' columns found (export the PLC tag table from the PLC engineering software)")
	}
	var out [][4]string
	for _, r := range rows[head+1:] {
		var x [4]string
		for k, c := range col {
			if c >= 0 && c < len(r) {
				x[k] = r[c]
			}
		}
		out = append(out, x)
	}
	return out, nil
}

// xlsxRows reads the first worksheet of an .xlsx file.
func xlsxRows(data []byte) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not an .xlsx file: %w", err)
	}
	read := func(name string) ([]byte, error) {
		for _, f := range zr.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 64<<20))
			}
		}
		return nil, os.ErrNotExist
	}
	var shared []string
	if b, err := read("xl/sharedStrings.xml"); err == nil {
		var sst struct {
			SI []struct {
				T string `xml:"t"`
				R []struct {
					T string `xml:"t"`
				} `xml:"r"`
			} `xml:"si"`
		}
		if err := xml.Unmarshal(b, &sst); err != nil {
			return nil, err
		}
		for _, si := range sst.SI {
			s := si.T
			for _, r := range si.R {
				s += r.T
			}
			shared = append(shared, s)
		}
	}
	sheet := ""
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") && (sheet == "" || f.Name < sheet) {
			sheet = f.Name
		}
	}
	b, err := read(sheet)
	if err != nil {
		return nil, errors.New("the .xlsx file has no worksheet")
	}
	var ws struct {
		Rows []struct {
			C []struct {
				R  string `xml:"r,attr"`
				T  string `xml:"t,attr"`
				V  string `xml:"v"`
				IS struct {
					T string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(b, &ws); err != nil {
		return nil, err
	}
	var rows [][]string
	for _, r := range ws.Rows {
		var row []string
		for _, c := range r.C {
			col := 0
			for _, ch := range c.R {
				if ch < 'A' || ch > 'Z' {
					break
				}
				col = col*26 + int(ch-'A'+1)
			}
			col--
			if col < 0 {
				col = len(row)
			}
			v := c.V
			switch c.T {
			case "s":
				if n, err := strconv.Atoi(v); err == nil && n < len(shared) {
					v = shared[n]
				}
			case "inlineStr":
				v = c.IS.T
			}
			for len(row) <= col {
				row = append(row, "")
			}
			row[col] = v
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// xmlTagRows reads a PLC tag table exported as XML, in either form TIA
// Portal writes:
//
//   - the tag table's Export (<Tagtable><Tag type="Bool" addr="%I0.0"
//     remark="…">Name</Tag>…), also from STEP 7 V5 / older TIA versions;
//   - TIA Openness (<SW.Tags.PlcTag> with Name, DataTypeName and
//     LogicalAddress, and the comment as a MultilingualText).
func xmlTagRows(data []byte) ([][4]string, error) {
	d := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
	d.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		if l := strings.ToLower(label); l == "utf-8" || l == "utf8" || l == "us-ascii" {
			return in, nil
		}
		return nil, fmt.Errorf("character set %q is not supported (export as UTF-8)", label)
	}
	var (
		rows    [][4]string
		path    []string   // open elements
		cur     *[4]string // the tag being read
		curAt   int        // depth of its element
		text    strings.Builder
		comment bool // inside its Comment text
		root    string
	)
	attr := func(se xml.StartElement, names ...string) string {
		for _, a := range se.Attr {
			for _, n := range names {
				if strings.EqualFold(a.Name.Local, n) {
					return a.Value
				}
			}
		}
		return ""
	}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not a valid XML file: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := t.Name.Local
			if root == "" {
				root = n
			}
			path = append(path, n)
			text.Reset()
			switch {
			case cur == nil && n == "Tag":
				cur, curAt = &[4]string{"", attr(t, "type", "datatype"), attr(t, "addr", "address", "logicaladdress"), attr(t, "remark", "comment")}, len(path)
			case cur == nil && n == "SW.Tags.PlcTag":
				cur, curAt = &[4]string{}, len(path)
			case cur != nil && n == "MultilingualText":
				comment = attr(t, "CompositionName") == "Comment"
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			n := t.Name.Local
			s := strings.TrimSpace(text.String())
			if cur != nil {
				switch {
				case len(path) == curAt: // the tag ends
					if n == "Tag" {
						cur[0] = s
					}
					rows = append(rows, *cur)
					cur = nil
				case len(path) == curAt+2 && path[curAt] == "AttributeList":
					switch n {
					case "Name":
						cur[0] = s
					case "DataTypeName":
						cur[1] = s
					case "LogicalAddress":
						cur[2] = s
					}
				case n == "MultilingualText":
					comment = false
				case comment && n == "Text" && cur[3] == "" && s != "":
					cur[3] = s
				}
			}
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
			text.Reset()
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no PLC tags found in the XML (root element <%s>); export the PLC tag table from the PLC engineering software", root)
	}
	return rows, nil
}

// sdfTagRows reads a PLC tag table in the .sdf form (one quoted line per
// tag, no heading): name, address, data type, accessible from HMI, visible
// in HMI, retain, comment, …, writable from HMI.
func sdfTagRows(data []byte) ([][4]string, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))))
	r.FieldsPerRecord, r.LazyQuotes = -1, true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("not a valid .sdf file: %w", err)
	}
	var rows [][4]string
	for _, f := range recs {
		if len(f) < 3 {
			continue
		}
		row := [4]string{f[0], f[2], f[1], ""}
		if len(f) > 6 {
			row[3] = f[6]
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("no tags found in the .sdf file (name, address, data type per line)")
	}
	return rows, nil
}

func csvRows(data []byte) ([][]string, error) {
	text := strings.TrimPrefix(string(data), "\uFEFF")
	first, _, _ := strings.Cut(text, "\n")
	sep := ','
	for _, c := range []rune{'\t', ';'} {
		if strings.Count(first, string(c)) > strings.Count(first, string(sep)) {
			sep = c
		}
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma, r.FieldsPerRecord, r.LazyQuotes = sep, -1, true
	return r.ReadAll()
}

// ---- store ----

type symFile struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// SymbolStore keeps the imported files and the blocks made from them.
type SymbolStore struct {
	mu     sync.Mutex
	path   string // plc-symbols.json; "" = memory only
	files  []symFile
	dbNum  map[string]int // DB number given to a data block, by name
	blocks []SymBlock
	notes  []string
}

func NewSymbolStore(path string) *SymbolStore {
	s := &SymbolStore{path: path, dbNum: map[string]int{}}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			var saved struct {
				Files []symFile      `json:"files"`
				DBs   map[string]int `json:"db_numbers"`
			}
			if json.Unmarshal(b, &saved) == nil {
				s.files = saved.Files
				if saved.DBs != nil {
					s.dbNum = saved.DBs
				}
			}
		}
	}
	s.rebuild()
	return s
}

func symbolPath(configPath string) string {
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "plc-symbols.json")
}

// SymState is what the API returns.
type SymState struct {
	Files  []string   `json:"files"`
	Blocks []SymBlock `json:"blocks"`
	Notes  []string   `json:"notes"`
}

func (s *SymbolStore) State() SymState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := SymState{Files: []string{}, Blocks: append([]SymBlock{}, s.blocks...), Notes: append([]string{}, s.notes...)}
	for _, f := range s.files {
		st.Files = append(st.Files, f.Name)
	}
	return st
}

// Import adds a file (or replaces one of the same name). found lists the
// data blocks a scan found, to guess DB numbers by size.
func (s *SymbolStore) Import(name string, data []byte, found []PLCArea) (SymState, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." {
		return SymState{}, errors.New("file name missing")
	}
	if len(data) > 32<<20 {
		return SymState{}, errors.New("file too large")
	}
	// Check the file on its own first, so a bad file is refused.
	if _, _, err := parseFiles([]symFile{{name, data}}, nil); err != nil {
		return SymState{}, err
	}
	s.mu.Lock()
	replaced := false
	for i := range s.files {
		if strings.EqualFold(s.files[i].Name, name) {
			s.files[i].Data, replaced = data, true
		}
	}
	if !replaced {
		s.files = append(s.files, symFile{name, data})
	}
	s.rebuildLocked()
	s.guessLocked(found)
	s.rebuildLocked()
	err := s.saveLocked()
	s.mu.Unlock()
	return s.State(), err
}

// guessLocked gives data blocks without a number one: from their name
// (DB10, DB_10) or, if exactly one found data block of that size is left,
// that one.
func (s *SymbolStore) guessLocked(found []PLCArea) {
	used := map[int]bool{}
	for _, n := range s.dbNum {
		used[n] = true
	}
	nameRe := regexp.MustCompile(`(?i)^DB[ _]?(\d+)$`)
	for _, b := range s.blocks {
		if b.Kind != "db" || b.DB != 0 {
			continue
		}
		if m := nameRe.FindStringSubmatch(b.Name); m != nil {
			n, _ := strconv.Atoi(m[1])
			if !used[n] {
				s.dbNum[b.Name], used[n] = n, true
				continue
			}
		}
		var match []int
		for _, f := range found {
			if f.Area == "DB" && f.Size == b.Size && !used[f.DB] {
				match = append(match, f.DB)
			}
		}
		if len(match) == 1 {
			s.dbNum[b.Name], used[match[0]] = match[0], true
		}
	}
}

// SetDB gives a data block its number (0 removes it).
func (s *SymbolStore) SetDB(block string, db int) error {
	if db < 0 || db > 65535 {
		return errors.New("DB number must be within 1..65535")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := false
	for _, b := range s.blocks {
		if b.Kind == "db" && b.Name == block {
			ok = true
		} else if db != 0 && b.Kind == "db" && b.DB == db {
			return fmt.Errorf("DB%d is already given to %s", db, b.Name)
		}
	}
	if !ok {
		return notFound("data block " + block + " not found")
	}
	if db == 0 {
		delete(s.dbNum, block)
	} else {
		s.dbNum[block] = db
	}
	s.rebuildLocked()
	return s.saveLocked()
}

// Remove drops a file, or all files when name is "".
func (s *SymbolStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		s.files, s.dbNum = nil, map[string]int{}
	} else {
		kept := s.files[:0]
		for _, f := range s.files {
			if f.Name != name {
				kept = append(kept, f)
			}
		}
		s.files = kept
	}
	s.rebuildLocked()
	return s.saveLocked()
}

func (s *SymbolStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(struct {
		Files []symFile      `json:"files"`
		DBs   map[string]int `json:"db_numbers"`
	}{s.files, s.dbNum})
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *SymbolStore) rebuild() {
	s.mu.Lock()
	s.rebuildLocked()
	s.mu.Unlock()
}

func (s *SymbolStore) rebuildLocked() {
	blocks, notes, _ := parseFiles(s.files, s.dbNum)
	s.blocks, s.notes = blocks, notes
}

// parseFiles makes blocks from all files together, so a data block can use
// a type from another file. The error is the first file that could not be
// read at all.
// isTagTable tells a PLC tag table file from a block source by its extension.
func isTagTable(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".xlsx", ".xml", ".sdf", ".csv", ".tsv":
		return true
	}
	return false
}

// tagTableRows reads a PLC tag table (.xlsx, .xml, .csv or .tsv) as rows of
// name, data type, address and comment.
func tagTableRows(name string, data []byte) ([][4]string, error) {
	var rows [][]string
	var err error
	switch strings.ToLower(filepath.Ext(name)) {
	case ".xml":
		return xmlTagRows(data)
	case ".sdf":
		return sdfTagRows(data)
	case ".xlsx":
		rows, err = xlsxRows(data)
	default:
		rows, err = csvRows(data)
	}
	if err != nil {
		return nil, err
	}
	return tableRows(rows)
}

// TIATags turns a PLC tag table exported from TIA Portal into tags of the
// I, Q and M areas, named and commented as in the PLC. Rows without an
// address or with a type that can't be a tag (String, DTL, …) are counted
// in skipped.
func TIATags(name string, data []byte) (tags []Tag, skipped int, err error) {
	if !isTagTable(name) {
		return nil, 0, errors.New("choose a PLC tag table exported from the PLC engineering software (.xlsx, .xml or .sdf; .csv also works)")
	}
	rows, err := tagTableRows(name, data)
	if err != nil {
		return nil, 0, err
	}
	for _, s := range tagTableSymbols(rows) {
		// A type given but unknown (String, DTL, Time, …): its bytes are not
		// a WORD or DWORD to show, so it is left out.
		if s.Type == "" || s.Type != "BOOL" && s.DataType != "" && tagTypeOf[strings.ToUpper(s.DataType)] == "" {
			continue
		}
		tags = append(tags, Tag{Name: s.Name, Area: s.Area, Offset: s.Offset, Bit: s.Bit, Type: s.Type, Description: s.Comment})
	}
	skipped = len(rows) - len(tags)
	if len(tags) == 0 {
		return nil, skipped, errors.New("the tag table has no tags with an I, Q or M address")
	}
	return tags, skipped, nil
}

func parseFiles(files []symFile, dbNum map[string]int) ([]SymBlock, []string, error) {
	types := map[string]*symType{}
	type pending struct {
		file string
		def  dbDef
	}
	var dbs []pending
	var blocks []SymBlock
	var notes []string
	var firstErr error
	fail := func(f string, err error) {
		notes = append(notes, f+": "+err.Error())
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", f, err)
		}
	}
	for _, f := range files {
		if isTagTable(f.Name) {
			tr, err := tagTableRows(f.Name, f.Data)
			if err != nil {
				fail(f.Name, err)
				continue
			}
			syms := tagTableSymbols(tr)
			b := SymBlock{Name: strings.TrimSuffix(f.Name, filepath.Ext(f.Name)), Kind: "tags", File: f.Name, Symbols: syms}
			if len(syms) == 0 {
				b.Note = "no tags with an address found"
			}
			blocks = append(blocks, b)
			continue
		}
		t, d, err := parseSource(string(f.Data))
		if err != nil {
			fail(f.Name, err)
			continue
		}
		if len(t) == 0 && len(d) == 0 {
			fail(f.Name, errors.New("no DATA_BLOCK or TYPE found (use Generate source from blocks in the PLC engineering software)"))
			continue
		}
		for k, v := range t {
			types[k] = v
		}
		for _, x := range d {
			dbs = append(dbs, pending{f.Name, x})
		}
	}
	for _, p := range dbs {
		d := p.def
		b := SymBlock{Name: d.name, Kind: "db", DB: d.number, File: p.file, Optimized: d.optimized, Symbols: []Symbol{}}
		if n, ok := dbNum[d.name]; ok {
			b.DB = n
		}
		typ := d.typ
		if typ == nil && d.ref != "" {
			typ = types[strings.ToUpper(d.ref)]
			if typ == nil && sysFBs[strings.ToUpper(d.ref)] != nil {
				typ = &symType{kind: kRef, name: d.ref}
			}
			if typ == nil {
				b.Note = fmt.Sprintf("made from %s, whose source is not imported: import the function block or PLC data type too "+
					"(Generate source from blocks → Including all dependent blocks)", d.ref)
			} else if typ.kind == kFB && typ.optimized {
				d.optimized = true // an instance DB has its FB's block access
				b.Optimized = true
			}
		}
		if typ != nil {
			l := &layouter{types: types, gap: -1}
			if err := l.place("", typ, "", 0); err != nil {
				b.Note = err.Error()
			} else {
				l.align(16)
				b.Size, b.Symbols = l.pos/8, l.out
				if l.cut {
					b.Note = fmt.Sprintf("only the first %d variables are listed", maxSymbols)
				}
			}
		}
		if d.optimized {
			msg := "optimized block access is on: PUT/GET cannot read this block (switch it off in the block's properties)"
			if b.Note != "" {
				msg += "; " + b.Note
			}
			b.Note = msg
		}
		for i := range b.Symbols {
			b.Symbols[i].Area = "DB"
		}
		blocks = append(blocks, b)
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].Kind != blocks[j].Kind {
			return blocks[i].Kind == "db"
		}
		if (blocks[i].DB == 0) != (blocks[j].DB == 0) {
			return blocks[i].DB != 0
		}
		if blocks[i].DB != blocks[j].DB {
			return blocks[i].DB < blocks[j].DB
		}
		return blocks[i].Name < blocks[j].Name
	})
	return blocks, notes, firstErr
}
