package main

// A PLC data type (UDT) written as a sheet (.xlsx, .xls, .csv): one row per
// member with its name and data type, nested members with dotted names:
//
//	Name          Data Type            Offset  Comment
//	Motor         Struct
//	Motor.Speed   Int                  0.0     rpm
//	Motor.Run     Bool                 2.0
//	Values        Array[0..3] of Real
//
// The Offset column is optional: without it the members are laid out by the
// rules of standard (non-optimized) access, as for a .udt source. The page
// then gives the data block number and the byte where the UDT starts.
//
// The same members (UDTMember) are what the UDT tool window edits; LayoutUDT
// lays them out and writes the TYPE source.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	sclIdentRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	udtOffsetRe = regexp.MustCompile(`^(\d+)(?:\.(\d))?$`)
)

// UDTMember is one member of a UDT: a dotted name inside it (Motor.Speed),
// its data type as written in SCL (Bool, Int, Array[0..7] of Bool,
// String[20], Struct for a group) and comment. Offset ("2.0") is given by a
// sheet or filled in by LayoutUDT; From is the tag a member was made from.
type UDTMember struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Comment string `json:"comment,omitempty"`
	Offset  string `json:"offset,omitempty"`
	From    string `json:"from,omitempty"`
}

// udtSheetMembers reads the members of a UDT sheet. ok is false when the
// sheet is not a UDT (a tag table has addresses). allOff: every member has
// its offset.
func udtSheetMembers(rows [][]string) (ms []UDTMember, allOff, ok bool) {
	const (
		cName = iota
		cType
		cOffset
		cComment
		nCols
	)
	col := [nCols]int{-1, -1, -1, -1}
	head := -1
	for i := 0; i < len(rows) && i < 20 && head < 0; i++ {
		c := [nCols]int{-1, -1, -1, -1}
		addr := false
		for j, h := range rows[i] {
			k := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(h)))
			set := func(n int) {
				if c[n] < 0 {
					c[n] = j
				}
			}
			switch k {
			case "name", "member", "membername", "variable", "element":
				set(cName)
			case "datatype", "type":
				set(cType)
			case "offset", "byteoffset", "addressoffset", "relativeaddress":
				set(cOffset)
			case "comment", "description", "desc", "remark":
				set(cComment)
			case "address", "logicaladdress", "s7address", "plcaddress", "area", "absoluteaddress", "operand", "db":
				addr = true // a tag table or a tag list, not a UDT
			}
		}
		if c[cName] >= 0 && c[cType] >= 0 {
			if addr {
				return nil, false, false
			}
			col, head = c, i
		}
	}
	if head < 0 {
		return nil, false, false
	}
	cell := func(r []string, k int) string {
		if c := col[k]; c >= 0 && c < len(r) {
			return strings.TrimSpace(r[c])
		}
		return ""
	}
	allOff = col[cOffset] >= 0
	for _, r := range rows[head+1:] {
		n, t := cell(r, cName), cell(r, cType)
		if n == "" || t == "" || strings.EqualFold(t, "END_STRUCT") {
			continue // notes, empty lines
		}
		m := UDTMember{Name: strings.Trim(n, `". `), Type: t, Comment: cell(r, cComment)}
		if o := cell(r, cOffset); udtOffsetRe.MatchString(o) {
			m.Offset = o
		} else if !strings.EqualFold(t, "Struct") {
			allOff = false
		}
		ms = append(ms, m)
	}
	return ms, allOff, true
}

var (
	udtNameBad = regexp.MustCompile(`[^\p{L}\p{N}_]+`)
	udtArrayRe = regexp.MustCompile(`(?i)^Array\s*\[[^\]]+\]\s+of\s+(.+)$`)
	udtStrRe   = regexp.MustCompile(`(?i)^W?String(\s*\[\s*\d+\s*\])?$`)
)

// udtName makes a block or type name from a file or a typed name.
func udtName(s string) string {
	s = strings.Trim(udtNameBad.ReplaceAllString(s, "_"), "_")
	if s == "" {
		return "UDT"
	}
	return s
}

// udtTypeCheck tells whether a member's data type can be written in the
// UDT: an elementary type, String[n], Array[…] of one of them, or Struct.
func udtTypeCheck(t string) error {
	s := strings.TrimSpace(t)
	for i := 0; i < 4; i++ {
		if m := udtArrayRe.FindStringSubmatch(s); m != nil {
			s = strings.TrimSpace(m[1])
		}
	}
	u := strings.ToUpper(s)
	switch {
	case u == "STRUCT" && s == strings.TrimSpace(t), udtStrRe.MatchString(s):
		return nil
	case strings.HasPrefix(s, `"`):
		return fmt.Errorf("another UDT (%s) can't be used here – write its members as a Struct", s)
	case u == "STRUCT":
		return fmt.Errorf("an array of Struct can't be written here – use another UDT in the PLC software")
	}
	if _, ok := elemSize[u]; ok {
		return nil
	}
	if a, ok := typeAlias[u]; ok {
		return fmt.Errorf("%q is not an SCL type – use %s", s, sclTypeNames[a])
	}
	return fmt.Errorf("unknown data type %q (Bool, Byte, Int, Word, DInt, Real, String[20], Array[0..7] of Bool, Struct, …)", s)
}

// UDTSheet reads a UDT sheet as one block (named after the file), ready for
// the Data blocks window. ok is false when the sheet is not a UDT: a tag table
// (it has addresses) or another layout.
func UDTSheet(name string, data []byte) (blocks []SourceBlock, ok bool, err error) {
	rows, err := sheetRows(name, data)
	if err != nil || rows == nil {
		return nil, false, nil
	}
	ms, allOff, ok := udtSheetMembers(rows)
	if !ok {
		return nil, false, nil
	}
	if len(ms) == 0 {
		return nil, true, fmt.Errorf("the UDT sheet has no members (columns Name and Data Type)")
	}
	block := udtName(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))

	if allOff { // every member has its offset: take them as written
		sb := SourceBlock{Name: block, Tags: []Tag{}}
		for _, m := range ms {
			if strings.EqualFold(m.Type, "Struct") {
				continue
			}
			dt := strings.ToUpper(m.Type)
			if a, ok := typeAlias[dt]; ok {
				dt = a
			}
			typ := tagTypeOf[dt]
			if typ == "" {
				sb.Other++
				continue
			}
			x := udtOffsetRe.FindStringSubmatch(m.Offset)
			off, _ := strconv.Atoi(x[1])
			tg := Tag{Name: block + "." + m.Name, Area: "DB", Offset: off, Type: typ, Description: m.Comment}
			if typ == "BOOL" {
				tg.Bit, _ = strconv.Atoi(x[2])
			}
			sb.Tags = append(sb.Tags, tg)
			if end := off + max(1, typeNames[typ].Size()); end > sb.Size {
				sb.Size = end
			}
		}
		sb.Size += sb.Size % 2
		return []SourceBlock{sb}, true, nil
	}

	// Without offsets: lay it out like a UDT source.
	lay, err := LayoutUDT(block, ms)
	if err != nil {
		return nil, true, fmt.Errorf("the UDT sheet: %w", err)
	}
	sb := SourceBlock{Name: block, Size: lay.Size, Other: lay.Other, Tags: []Tag{}, UDT: lay.SCL}
	for _, t := range lay.Tags {
		t.Name = block + "." + t.Name
		sb.Tags = append(sb.Tags, t)
	}
	return []SourceBlock{sb}, true, nil
}

var udtIndexRe = regexp.MustCompile(`\[\s*(\d+)\s*\]`)

// udtSegName makes a member name the PLC software accepts: an element of
// an array left on its own (Alarms[4], from a tag) becomes Alarms_4.
func udtSegName(s string) string {
	s = udtIndexRe.ReplaceAllString(strings.TrimSpace(s), "_$1")
	return strings.NewReplacer("[", "_", "]", "_", `"`, "'").Replace(s)
}

// udtPath is a dotted member name as it is written in the TYPE.
func udtPath(name string) string {
	parts := strings.Split(strings.Trim(name, `". `), ".")
	for i, p := range parts {
		parts[i] = udtSegName(p)
	}
	return strings.Join(parts, ".")
}

// udtTypeBody writes the members as the STRUCT … END_STRUCT; END_TYPE part of
// a TYPE: dotted names become nested structures.
func udtTypeBody(ms []UDTMember) string {
	type node struct {
		name, typ, comment string
		kids               []*node
		byName             map[string]*node
	}
	root := &node{byName: map[string]*node{}}
	for _, m := range ms {
		cur := root
		path := strings.Split(strings.Trim(m.Name, `". `), ".")
		for i, p := range path {
			p = strings.TrimSpace(p)
			nx := cur.byName[p]
			if nx == nil {
				nx = &node{name: p, byName: map[string]*node{}}
				cur.byName[p] = nx
				cur.kids = append(cur.kids, nx)
			}
			cur = nx
			if i == len(path)-1 {
				cur.comment = m.Comment
				if !strings.EqualFold(m.Type, "Struct") {
					cur.typ = strings.TrimSpace(m.Type)
				}
			}
		}
	}
	quote := func(s string) string {
		if sclIdentRe.MatchString(s) {
			return s
		}
		return `"` + strings.ReplaceAll(s, `"`, "'") + `"`
	}
	var b strings.Builder
	var write func(n *node, ind string)
	write = func(n *node, ind string) {
		cm := ""
		if n.comment != "" {
			cm = "   // " + strings.ReplaceAll(n.comment, "\n", " ")
		}
		n.name = udtSegName(n.name)
		if len(n.kids) > 0 {
			fmt.Fprintf(&b, "%s%s : Struct%s\n", ind, quote(n.name), cm)
			for _, k := range n.kids {
				write(k, ind+"   ")
			}
			fmt.Fprintf(&b, "%sEND_STRUCT;\n", ind)
			return
		}
		if n.typ == "" {
			return // an empty Struct
		}
		fmt.Fprintf(&b, "%s%s : %s;%s\n", ind, quote(n.name), n.typ, cm)
	}
	b.WriteString("   STRUCT\n")
	for _, k := range root.kids {
		write(k, "      ")
	}
	b.WriteString("   END_STRUCT;\nEND_TYPE\n")
	return b.String()
}

// UDTLayoutResult is a laid-out UDT: its size, the members with their
// offsets, the tags it gives (names inside the UDT) and the TYPE source.
type UDTLayoutResult struct {
	Name    string      `json:"name"`
	Size    int         `json:"size"`
	Members []UDTMember `json:"members"`
	Tags    []Tag       `json:"tags"`
	Other   int         `json:"other"` // members that can't be tags (String, …)
	SCL     string      `json:"scl"`
}

// LayoutUDT lays the members out by the rules of standard access.
func LayoutUDT(name string, ms []UDTMember) (UDTLayoutResult, error) {
	name = udtName(name)
	res := UDTLayoutResult{Name: name, Members: append([]UDTMember{}, ms...), Tags: []Tag{}}
	if len(ms) == 0 {
		return res, fmt.Errorf("the UDT has no members")
	}
	for i, m := range ms {
		for _, p := range strings.Split(m.Name, ".") {
			if strings.TrimSpace(p) == "" {
				return res, fmt.Errorf("member %d: the name %q has an empty part", i+1, m.Name)
			}
		}
		if strings.TrimSpace(m.Type) == "" {
			return res, fmt.Errorf("member %s: no data type", m.Name)
		}
		if err := udtTypeCheck(m.Type); err != nil {
			return res, fmt.Errorf("member %s: %w", m.Name, err)
		}
	}
	body := udtTypeBody(ms)
	// Laid out through a data block of the type (named apart from the type).
	src := fmt.Sprintf("TYPE \"%s_T\"\nVERSION : 0.1\n%s\nDATA_BLOCK \"%s\"\n{ S7_Optimized_Access := 'FALSE' }\nVERSION : 0.1\nNON_RETAIN\n\"%s_T\"\nBEGIN\nEND_DATA_BLOCK\n", name, body, name, name)
	blocks, err := SourceTags(name+".scl", []byte(src), nil, nil)
	if err != nil {
		return res, err
	}
	b := blocks[0]
	res.Size, res.Other = b.Size, b.Other
	for _, t := range b.Tags {
		t.Name = strings.TrimPrefix(t.Name, name+".")
		res.Tags = append(res.Tags, t)
	}
	// Each member's offset: its first tag (a struct or array starts there).
	for i, m := range res.Members {
		p := udtPath(m.Name)
		res.Members[i].Offset = ""
		for _, t := range res.Tags {
			if t.Name == p || strings.HasPrefix(t.Name, p+".") || strings.HasPrefix(t.Name, p+"[") {
				res.Members[i].Offset = fmt.Sprintf("%d.%d", t.Offset, t.Bit)
				break
			}
		}
	}
	res.SCL = fmt.Sprintf("TYPE \"%s\"\nVERSION : 0.1\n%s", name, body)
	return res, nil
}
