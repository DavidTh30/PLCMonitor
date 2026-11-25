package main

// Export of the tag list as a TIA Portal PLC tag table (the reverse of
// TIATags): .xlsx in the layout of TIA Portal's tag table Export/Import
// (sheet "PLC Tags"), or .xml in the <Tagtable> form TIA Portal writes.
// A PLC tag table holds only inputs, outputs and memory, so DB, Modbus and
// OPC UA tags are left out.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// tiaTypeNames are the TIA Portal names of the tag types.
var tiaTypeNames = map[string]string{
	"BOOL": "Bool", "BYTE": "Byte", "USINT": "USInt", "SINT": "SInt",
	"WORD": "Word", "UINT": "UInt", "INT": "Int",
	"DWORD": "DWord", "UDINT": "UDInt", "DINT": "DInt", "REAL": "Real",
}

// TIARow is one tag of an exported tag table.
type TIARow struct {
	Name, DataType, Address, Comment string
}

// TIASkipped is a tag that can't be in a PLC tag table, and why.
type TIASkipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// tiaRows picks the tags a PLC tag table can hold.
func tiaRows(tags []Tag) (rows []TIARow, skipped []TIASkipped) {
	for _, t := range tags {
		typ := strings.ToUpper(t.Type)
		area := strings.ToUpper(t.Area)
		reason := ""
		switch {
		case area == "DB":
			reason = "a data block variable (it belongs in the DB, not in a tag table)"
		case area != "I" && area != "Q" && area != "M":
			reason = "not an input, output or memory address of the PLC (" + t.Area + ")"
		case typ == "LREAL":
			reason = "LReal can't have an I/Q/M address in a tag table"
		case tiaTypeNames[typ] == "":
			reason = "type " + t.Type + " has no equivalent in a PLC tag table"
		}
		if reason != "" {
			skipped = append(skipped, TIASkipped{t.Name, reason})
			continue
		}
		var addr string
		if typ == "BOOL" {
			addr = fmt.Sprintf("%%%s%d.%d", area, t.Offset, t.Bit)
		} else {
			w := map[int]string{1: "B", 2: "W", 4: "D"}[typeNames[typ].Size()]
			addr = fmt.Sprintf("%%%s%s%d", area, w, t.Offset)
		}
		// TIA Portal names: no double quotes, at most 128 characters.
		name := strings.ReplaceAll(t.Name, `"`, "'")
		if r := []rune(name); len(r) > 128 {
			name = string(r[:128])
		}
		rows = append(rows, TIARow{name, tiaTypeNames[typ], addr, t.Description})
	}
	return rows, skipped
}

// TIAExport writes the tags as a PLC tag table, format "xlsx" or "xml".
func TIAExport(tags []Tag, format string) (data []byte, rows int, skipped []TIASkipped, err error) {
	r, skipped := tiaRows(tags)
	if len(r) == 0 {
		return nil, 0, skipped, errors.New("no tags with an I, Q or M address to export (a PLC tag table can't hold DB, Modbus or OPC UA tags)")
	}
	switch format {
	case "xlsx":
		data, err = tiaXLSX(r)
	case "xml":
		data = tiaXML(r)
	case "sdf":
		data = tiaSDF(r)
	default:
		err = fmt.Errorf("format must be xlsx, xml or sdf, not %q", format)
	}
	return data, len(r), skipped, err
}

// tiaXML writes the <Tagtable> form, as TIA Portal exports a tag table.
func tiaXML(rows []TIARow) []byte {
	esc := func(s string) string {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var b bytes.Buffer
	b.WriteString("<?xml version='1.0' encoding='utf-8'?>\r\n<Tagtable name='Default tag table'>\r\n")
	b.WriteString("  <!-- exported by PLC Monitor " + esc(version) + " -->\r\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  <Tag type='%s' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='%s' addr='%s'>%s</Tag>\r\n",
			esc(r.DataType), esc(r.Comment), esc(r.Address), esc(r.Name))
	}
	b.WriteString("</Tagtable>\r\n")
	return b.Bytes()
}

// tiaSDF writes the .sdf form: one quoted line per tag – name, address,
// data type, accessible from HMI, visible in HMI, retain, comment, two
// empty fields, writable from HMI.
func tiaSDF(rows []TIARow) []byte {
	q := func(s string) string {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `"`, `""`), "\n", " ") + `"`
	}
	var b bytes.Buffer
	for _, r := range rows {
		fmt.Fprintf(&b, "%s,%s,%s,\"True\",\"True\",\"False\",%s,\"\",\"True\"\r\n", q(r.Name), q(r.Address), q(r.DataType), q(r.Comment))
	}
	return b.Bytes()
}

// tiaXLSXHead are the columns of TIA Portal's tag table export.
var tiaXLSXHead = []string{"Name", "Path", "Data Type", "Logical Address", "Comment",
	"Hmi Visible", "Hmi Accessible", "Hmi Writeable", "Typeobject ID", "Version ID"}

// tiaXLSX writes a workbook with the sheet "PLC Tags" (shared strings, as
// Excel and TIA Portal write it).
func tiaXLSX(rows []TIARow) ([]byte, error) {
	table := [][]any{}
	for _, r := range rows {
		table = append(table, []any{r.Name, "Default tag table", r.DataType, r.Address, r.Comment, "True", "True", "True", "", ""})
	}
	return xlsxWorkbook("PLC Tags", tiaXLSXHead, table, []float64{32, 20, 16, 16, 40})
}

// xlsxWorkbook writes a workbook with one sheet: a heading row (bold, kept
// at the top) and rows of strings and numbers (int, float64). widths are
// the first columns' widths in characters.
func xlsxWorkbook(sheetName string, head []string, rows [][]any, widths []float64) ([]byte, error) {
	var strs []string
	idx := map[string]int{}
	str := func(s string) int {
		if i, ok := idx[s]; ok {
			return i
		}
		idx[s] = len(strs)
		strs = append(strs, s)
		return len(strs) - 1
	}
	esc := func(s string) string {
		var b bytes.Buffer
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	col := func(i int) string {
		if i < 26 {
			return string(rune('A' + i))
		}
		return string(rune('A'+i/26-1)) + string(rune('A'+i%26))
	}

	var sheet bytes.Buffer
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	if len(widths) > 0 {
		sheet.WriteString(`<cols>`)
		for i, w := range widths {
			fmt.Fprintf(&sheet, `<col min="%d" max="%d" width="%g" customWidth="1"/>`, i+1, i+1, w)
		}
		sheet.WriteString(`</cols>`)
	}
	sheet.WriteString(`<sheetData>`)
	cells := func(n int, vals []any, style int) {
		fmt.Fprintf(&sheet, `<row r="%d">`, n)
		for i, v := range vals {
			s := ""
			if style > 0 {
				s = fmt.Sprintf(` s="%d"`, style)
			}
			switch x := v.(type) {
			case string:
				if x != "" {
					fmt.Fprintf(&sheet, `<c r="%s%d" t="s"%s><v>%d</v></c>`, col(i), n, s, str(x))
				}
			case int:
				fmt.Fprintf(&sheet, `<c r="%s%d"%s><v>%d</v></c>`, col(i), n, s, x)
			case float64:
				fmt.Fprintf(&sheet, `<c r="%s%d"%s><v>%s</v></c>`, col(i), n, s, strconv.FormatFloat(x, 'g', -1, 64))
			}
		}
		sheet.WriteString(`</row>`)
	}
	hv := make([]any, len(head))
	for i, h := range head {
		hv[i] = h
	}
	cells(1, hv, 1)
	for i, r := range rows {
		cells(i+2, r, 0)
	}
	sheet.WriteString(`</sheetData></worksheet>`)

	var ss bytes.Buffer
	fmt.Fprintf(&ss, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+"\n"+
		`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, len(strs), len(strs))
	for _, s := range strs {
		ss.WriteString(`<si><t xml:space="preserve">` + esc(s) + `</t></si>`)
	}
	ss.WriteString(`</sst>`)

	files := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
			`<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>` +
			`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
			`</Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
			`</Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="` + esc(sheetName) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>` +
			`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
			`</Relationships>`},
		{"xl/styles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
			`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
			`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
			`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
			`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
			`<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>` +
			`</styleSheet>`},
		{"xl/worksheets/sheet1.xml", sheet.String()},
		{"xl/sharedStrings.xml", ss.String()},
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
