package main

// The tag list as an Excel workbook for PLC Monitor itself (and for people
// who work on it): every tag – DB, I/Q/M, Modbus, OPC UA – with every field,
// so it can be edited in Excel and imported again without losing anything.
// Unlike the PLC tag table export, nothing is left out.
//
// Columns: Name, Address (as shown, read only), Area, DB, Offset, Bit, Type,
// Unit, Deadband, Description, Node. On import the fields count; Address is
// only for reading.

import (
	"fmt"
	"strconv"
	"strings"
)

// tagListHead are the columns of the PLC Monitor tag list.
var tagListHead = []string{"Name", "Address", "Area", "DB", "Offset", "Bit", "Type", "Unit", "Deadband", "Description", "Node"}

// TagListXLSX writes all tags with every field.
func TagListXLSX(tags []Tag) ([]byte, int, error) {
	if len(tags) == 0 {
		return nil, 0, fmt.Errorf("the tag list is empty")
	}
	rows := make([][]any, 0, len(tags))
	for _, t := range tags {
		addr := ""
		c := t
		if a, err := parseArea(c.Area); err == nil && c.normalize(a.Protocol()) == nil {
			addr = c.Address()
		}
		var dead any = ""
		if t.Deadband != 0 {
			dead = t.Deadband
		}
		rows = append(rows, []any{t.Name, addr, strings.ToUpper(t.Area), t.DB, t.Offset, t.Bit, strings.ToUpper(t.Type),
			t.Unit, dead, t.Description, t.Node})
	}
	data, err := xlsxWorkbook("PLC Monitor tags", tagListHead, rows, []float64{30, 18, 7, 6, 8, 5, 8, 6, 10, 44, 30})
	return data, len(rows), err
}

// fieldTagTable reads a sheet in the PLC Monitor tag list layout (columns
// name, area, type, offset, … as the .xlsx or CSV export writes them). ok is
// false when the sheet has another layout (a PLC tag table, a register map).
func fieldTagTable(rows [][]string) (tags []Tag, skipped []SkippedRow, ok bool) {
	col := map[string]int{}
	head := -1
	for i := 0; i < len(rows) && i < 10; i++ {
		c := map[string]int{}
		for j, h := range rows[i] {
			k := strings.ToLower(strings.TrimSpace(h))
			if _, dup := c[k]; !dup {
				c[k] = j
			}
		}
		_, n := c["name"]
		_, a := c["area"]
		_, t := c["type"]
		_, o := c["offset"]
		if n && a && t && o {
			col, head = c, i
			break
		}
	}
	if head < 0 {
		return nil, nil, false
	}
	cell := func(r []string, k string) string {
		if j, ok := col[k]; ok && j < len(r) {
			return strings.TrimSpace(r[j])
		}
		return ""
	}
	num := func(r []string, k string) (int, bool) {
		s := cell(r, k)
		if s == "" {
			return 0, true
		}
		f, err := strconv.ParseFloat(s, 64)
		return int(f), err == nil && f == float64(int(f))
	}
	for i, r := range rows[head+1:] {
		where := "row " + strconv.Itoa(head+2+i)
		t := Tag{Name: cell(r, "name"), Area: strings.ToUpper(cell(r, "area")), Type: strings.ToUpper(cell(r, "type")),
			Description: cell(r, "description"), Node: cell(r, "node")}
		if t.Area == "" && t.Type == "" {
			continue // a note or an empty line
		}
		skip := func(why string) {
			skipped = append(skipped, SkippedRow{where, t.Name, cell(r, "address"), why})
		}
		var good bool
		var bad []string
		for _, f := range []struct {
			k string
			p *int
		}{{"db", &t.DB}, {"offset", &t.Offset}, {"bit", &t.Bit}, {"unit", &t.Unit}} {
			if *f.p, good = num(r, f.k); !good {
				bad = append(bad, f.k+" "+strconv.Quote(cell(r, f.k)))
			}
		}
		if s := cell(r, "deadband"); s != "" {
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				t.Deadband = v
			} else {
				bad = append(bad, "deadband "+strconv.Quote(s))
			}
		}
		if len(bad) > 0 {
			skip("not a number: " + strings.Join(bad, ", "))
			continue
		}
		if t.Name == "" {
			skip("no name")
			continue
		}
		c := t
		a, err := parseArea(c.Area)
		if err == nil {
			err = c.normalize(a.Protocol())
		}
		if err != nil {
			skip(err.Error())
			continue
		}
		tags = append(tags, t)
	}
	return tags, skipped, true
}

// TagListImport reads a PLC Monitor tag list (.xlsx, .xls, .csv, .tsv with
// the columns name, area, type, offset, …). ok is false for other files.
func TagListImport(name string, data []byte) (tags []Tag, skipped []SkippedRow, ok bool, err error) {
	rows, err := sheetRows(name, data)
	if err != nil || rows == nil {
		return nil, nil, false, nil // not a sheet: read as a PLC tag table
	}
	tags, skipped, ok = fieldTagTable(rows)
	if ok && len(tags) == 0 {
		return nil, skipped, true, fmt.Errorf("the tag list has no tags that can be read")
	}
	return tags, skipped, ok, nil
}
