package main

// SCL sources and the tag list, both ways:
//
//   - SourceTags reads a source generated from the PLC's blocks (.scl, .db,
//     .udt; data blocks, PLC data types, function blocks for instance DBs)
//     and gives every data block with its variables, ready to become tags;
//   - TagsToSCL writes the DB tags of the tag list as SCL data blocks with
//     standard access, padded so that every tag keeps its address – for
//     "Generate blocks from source" in the PLC engineering software.

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// isBlockSource tells a block source by its extension.
func isBlockSource(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".scl", ".db", ".udt", ".awl":
		return true
	}
	return false
}

// SourceBlock is a data block of a source, with the variables that can be
// tags (in order) and the number it has in the PLC, when known.
type SourceBlock struct {
	Name      string `json:"name"`
	DB        int    `json:"db"` // 0: not known
	Size      int    `json:"size"`
	Optimized bool   `json:"optimized,omitempty"`
	Note      string `json:"note,omitempty"`
	Tags      []Tag  `json:"tags"`
	Other     int    `json:"other"` // variables that can't be tags (String, pointers, …)
	// UDT: a UDT sheet's type as SCL (TYPE "Name" … END_TYPE), for Save as SCL.
	UDT string `json:"udt,omitempty"`
}

// UDTDataBlockSCL writes a UDT's type and a data block that holds it at
// start (bytes before it are a Byte array), once or as Array[from..from+count-1].
// The members keep the addresses the Data blocks window showed.
func UDTDataBlockSCL(udt, typeName, dbName, member string, start, count, from int) (string, error) {
	if !strings.Contains(udt, "END_TYPE") || typeName == "" {
		return "", errors.New("no UDT to write")
	}
	if start < 0 || start%2 != 0 {
		return "", errors.New("the start byte must be even")
	}
	quote := func(s string) string {
		if sclIdentRe.MatchString(s) {
			return s
		}
		return `"` + strings.ReplaceAll(s, `"`, "'") + `"`
	}
	if dbName == "" {
		dbName = typeName + "_DB"
	}
	if member == "" {
		member = typeName
	}
	var b strings.Builder
	b.WriteString("// Written by PLC Monitor " + version + "\n")
	b.WriteString(strings.TrimRight(udt, "\n") + "\n\n")
	fmt.Fprintf(&b, "DATA_BLOCK \"%s\"\n{ S7_Optimized_Access := 'FALSE' }\nVERSION : 0.1\nNON_RETAIN\n   STRUCT\n", strings.ReplaceAll(dbName, `"`, "'"))
	if start > 0 {
		fmt.Fprintf(&b, "      Spare_0 : Array[0..%d] of Byte;   // bytes before the UDT\n", start-1)
	}
	if count > 1 {
		fmt.Fprintf(&b, "      %s : Array[%d..%d] of \"%s\";\n", quote(member), from, from+count-1, typeName)
	} else {
		fmt.Fprintf(&b, "      %s : \"%s\";\n", quote(member), typeName)
	}
	b.WriteString("   END_STRUCT;\n\nBEGIN\nEND_DATA_BLOCK\n")
	return b.String(), nil
}

// SourceTags reads a block source. dbNum gives known DB numbers by block
// name; found (a Scan PLC result) fills in the others by size when exactly
// one unused data block of the PLC has that size.
func SourceTags(name string, data []byte, dbNum map[string]int, found []PLCArea) ([]SourceBlock, error) {
	if !isBlockSource(name) {
		return nil, errors.New("choose a source generated from the PLC's blocks (.scl, .db, .udt)")
	}
	blocks, notes, err := parseFiles([]symFile{{name, data}}, dbNum)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, b := range blocks {
		if b.DB != 0 {
			used[b.DB] = true
		}
	}
	var out []SourceBlock
	for _, b := range blocks {
		if b.Kind != "db" {
			continue
		}
		sb := SourceBlock{Name: b.Name, DB: b.DB, Size: b.Size, Optimized: b.Optimized, Note: b.Note, Tags: []Tag{}}
		if sb.DB == 0 && sb.Size > 0 {
			var match []int
			for _, f := range found {
				if f.Area == "DB" && f.Size == sb.Size && !used[f.DB] {
					match = append(match, f.DB)
				}
			}
			if len(match) == 1 {
				sb.DB, used[match[0]] = match[0], true
			}
		}
		for _, s := range b.Symbols {
			if s.Type == "" {
				sb.Other++
				continue
			}
			sb.Tags = append(sb.Tags, Tag{Name: b.Name + "." + s.Name, Area: "DB", DB: sb.DB, Offset: s.Offset, Bit: s.Bit,
				Type: s.Type, Description: s.Comment})
		}
		out = append(out, sb)
	}
	if len(out) == 0 {
		msg := "no DATA_BLOCK in the source"
		if len(notes) > 0 {
			msg += ": " + strings.Join(notes, "; ")
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

// sclTypeNames are the SCL names of the tag types.
var sclTypeNames = map[string]string{
	"BOOL": "Bool", "BYTE": "Byte", "USINT": "USInt", "SINT": "SInt", "WORD": "Word", "UINT": "UInt", "INT": "Int",
	"DWORD": "DWord", "UDINT": "UDInt", "DINT": "DInt", "REAL": "Real", "LREAL": "LReal",
}

var sclBadChars = regexp.MustCompile(`[^\p{L}\p{N}_ \-]+`)

// sclName makes a member name from a tag name: without the block's own name
// in front, and without characters a name can't hold (".", "[", …).
func sclName(tag, block string) string {
	n := tag
	for _, p := range []string{block + ".", "DB" + block + "."} {
		if strings.HasPrefix(n, p) && len(n) > len(p) {
			n = n[len(p):]
		}
	}
	n = strings.Trim(sclBadChars.ReplaceAllString(n, "_"), "_ ")
	if n == "" {
		n = "Tag"
	}
	if r := []rune(n); len(r) > 120 {
		n = string(r[:120])
	}
	return n
}

type sclMember struct {
	name, typ, comment string
	start              int // bit
	tag                bool
}

// TagsToSCL writes the DB tags as SCL data blocks. names gives a block's
// name by DB number (e.g. from imported sources); others are named DB<n>.
// Tags that overlap one written before, 16-bit or longer values at an odd
// byte, and tags outside data blocks are left out with the reason.
func TagsToSCL(tags []Tag, names map[int]string) (src []byte, count int, skipped []TIASkipped, err error) {
	byDB := map[int][]Tag{}
	for _, t := range tags {
		typ := strings.ToUpper(t.Type)
		switch {
		case strings.ToUpper(t.Area) != "DB":
			skipped = append(skipped, TIASkipped{t.Name, "not in a data block (" + t.Area + "); inputs, outputs and memory go into a PLC tag table"})
		case sclTypeNames[typ] == "":
			skipped = append(skipped, TIASkipped{t.Name, "type " + t.Type + " has no SCL equivalent"})
		default:
			byDB[t.DB] = append(byDB[t.DB], t)
		}
	}
	if len(byDB) == 0 {
		return nil, 0, skipped, errors.New("no tags in data blocks to write (an SCL source holds data blocks; I, Q and M tags go into a PLC tag table)")
	}
	dbs := make([]int, 0, len(byDB))
	for n := range byDB {
		dbs = append(dbs, n)
	}
	sort.Ints(dbs)

	var b bytes.Buffer
	b.WriteString("// Data blocks of the tag list, written by PLC Monitor " + version + ".\r\n")
	b.WriteString("// In the PLC engineering software: External source files → Add new external file → this file,\r\n")
	b.WriteString("// then Generate blocks from source. Give each block the DB number in its name (block properties),\r\n")
	b.WriteString("// so that the addresses of the tags stay as they are. Spare_… members keep the gaps.\r\n\r\n")
	for _, n := range dbs {
		list := byDB[n]
		sort.SliceStable(list, func(i, j int) bool { return tagStartBit(list[i]) < tagStartBit(list[j]) })
		var mem []sclMember
		pos := 0 // bit
		taken := map[string]bool{}
		block := names[n]
		if block == "" { // the tags' common first part (DB_HMI.Delay[0] → DB_HMI), else DB<n>
			first, _, _ := strings.Cut(list[0].Name, ".")
			for _, t := range list {
				if p, _, ok := strings.Cut(t.Name, "."); !ok || p != first {
					first = ""
					break
				}
			}
			if first != "" && sclName(first, "") == first {
				block = first
			} else {
				block = fmt.Sprintf("DB%d", n)
			}
		}
		tagNames := map[string]bool{} // names the tags will get: spares keep clear of them
		for _, t := range list {
			tagNames[strings.ToLower(sclName(t.Name, block))] = true
		}
		uniq := func(nm string) string {
			base := nm
			for i := 2; taken[strings.ToLower(nm)] || tagNames[strings.ToLower(nm)]; i++ {
				nm = fmt.Sprintf("%s_%d", base, i)
			}
			taken[strings.ToLower(nm)] = true
			return nm
		}
		spare := func(at int, typ string) {
			nm := fmt.Sprintf("Spare_%d", at/8)
			if typ == "Bool" {
				nm = fmt.Sprintf("Spare_%d_%d", at/8, at%8)
			}
			mem = append(mem, sclMember{name: uniq(nm), typ: typ, start: at})
		}
		fillBytes := func(from, to int) { // whole bytes [from, to)
			if from%2 == 1 && from < to {
				spare(from*8, "Byte")
				from++
			}
			if k := (to - from) &^ 1; k >= 2 {
				mem = append(mem, sclMember{name: uniq(fmt.Sprintf("Spare_%d", from)), typ: fmt.Sprintf("Array[0..%d] of Byte", k-1), start: from * 8})
				from += k
			}
			if from < to {
				spare(from*8, "Byte")
			}
		}
		for _, t := range list {
			typ := strings.ToUpper(t.Type)
			start := tagStartBit(t)
			size := typeNames[typ].Size()
			natural := pos
			switch {
			case typ == "BOOL":
			case size == 1:
				natural = (pos + 7) / 8 * 8
			default:
				natural = (pos + 15) / 16 * 16
			}
			if start < natural || size >= 2 && start%16 != 0 {
				why := "overlaps a tag before it"
				if start >= pos && size >= 2 {
					why = "a value of 2 bytes or more must start on an even byte in a data block with standard access"
				}
				skipped = append(skipped, TIASkipped{t.Name, why})
				continue
			}
			if typ == "BOOL" {
				if start/8 != pos/8 {
					// A Bool after a Bool shares its byte: fill that byte up first.
					for ; pos%8 != 0; pos++ {
						spare(pos, "Bool")
					}
					fillBytes(pos/8, start/8)
					pos = start / 8 * 8
				}
				for ; pos < start; pos++ {
					spare(pos, "Bool")
				}
			} else {
				fillBytes((pos+7)/8, start/8)
			}
			nm := sclName(t.Name, block)
			for i := 2; taken[strings.ToLower(nm)]; i++ {
				nm = fmt.Sprintf("%s_%d", sclName(t.Name, block), i)
			}
			taken[strings.ToLower(nm)] = true
			delete(tagNames, strings.ToLower(sclName(t.Name, block)))
			c := strings.Join(strings.Fields(t.Description), " ")
			if orig := t.Name; sclName(orig, block) != orig && !strings.HasPrefix(orig, block+".") {
				c = strings.TrimSpace(c + " (tag " + orig + ")")
			}
			mem = append(mem, sclMember{name: nm, typ: sclTypeNames[typ], comment: c, start: start, tag: true})
			count++
			if typ == "BOOL" {
				pos = start + 1
			} else {
				pos = start + size*8
			}
		}
		if !hasTag(mem) {
			continue
		}
		var db bytes.Buffer
		fmt.Fprintf(&db, "DATA_BLOCK %s\r\n{ S7_Optimized_Access := 'FALSE' }\r\nVERSION : 0.1\r\nNON_RETAIN\r\n   STRUCT \r\n", sclQuote(block))
		for _, m := range mem {
			fmt.Fprintf(&db, "      %s : %s;", sclQuote(m.name), m.typ)
			if m.comment != "" {
				db.WriteString("   // " + m.comment)
			}
			db.WriteString("\r\n")
		}
		db.WriteString("   END_STRUCT;\r\n\r\n\r\nBEGIN\r\n\r\nEND_DATA_BLOCK\r\n\r\n")
		// Check the layout with the importer: every tag where it was.
		if err := checkSCLLayout(db.Bytes(), block, mem); err != nil {
			return nil, 0, nil, fmt.Errorf("DB%d: %w", n, err)
		}
		b.Write(db.Bytes())
	}
	return b.Bytes(), count, skipped, nil
}

func hasTag(m []sclMember) bool {
	for _, x := range m {
		if x.tag {
			return true
		}
	}
	return false
}

func tagStartBit(t Tag) int {
	if strings.ToUpper(t.Type) == "BOOL" {
		return t.Offset*8 + t.Bit
	}
	return t.Offset * 8
}

// sclQuote writes a name in quotes, as the engineering software does.
func sclQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, "'") + `"` }

// checkSCLLayout lays the written block out again and compares.
func checkSCLLayout(src []byte, block string, mem []sclMember) error {
	blocks, notes, err := parseFiles([]symFile{{"check.db", src}}, nil)
	if err != nil {
		return err
	}
	if len(blocks) != 1 || blocks[0].Note != "" {
		return fmt.Errorf("written source does not read back: %v %v", notes, blocks)
	}
	at := map[string]int{}
	for _, s := range blocks[0].Symbols {
		at[s.Name] = s.Offset*8 + s.Bit
	}
	for _, m := range mem {
		if !m.tag {
			continue
		}
		if got, ok := at[m.name]; !ok || got != m.start {
			return fmt.Errorf("%s would be at bit %d instead of %d", m.name, got, m.start)
		}
	}
	return nil
}
