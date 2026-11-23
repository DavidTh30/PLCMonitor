package main

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestTIAExport(t *testing.T) {
	tags := []Tag{
		{Name: "Start_PB", Area: "I", Offset: 0, Bit: 3, Type: "BOOL", Description: `start <"button"> & more`},
		{Name: "Clock_Byte", Area: "M", Offset: 0, Type: "BYTE"},
		{Name: "Silo_Level_AI", Area: "M", Offset: 21, Type: "INT"},
		{Name: "AnalogToBlower", Area: "Q", Offset: 64, Type: "WORD"},
		{Name: "Total", Area: "M", Offset: 100, Type: "REAL"},
		{Name: "Count", Area: "M", Offset: 104, Type: "UDINT"},
		{Name: "Speed", Area: "DB", DB: 1, Offset: 2, Type: "INT"},
		{Name: "Precise", Area: "M", Offset: 200, Type: "LREAL"},
		{Name: "Holding", Area: "HR", Offset: 1, Type: "INT"},
		{Name: "UA", Area: "UA", Node: "ns=3;s=x", Type: "INT"},
	}
	for _, format := range []string{"xlsx", "xml", "sdf"} {
		data, n, skipped, err := TIAExport(tags, format)
		if err != nil {
			t.Fatal(format, err)
		}
		if n != 6 || len(skipped) != 4 {
			t.Errorf("%s: %d rows, skipped %+v", format, n, skipped)
		}
		// Read back with the importer: the same tags come out.
		back, sk, err := TIATags("PLCTags."+format, data)
		if err != nil || sk != 0 || len(back) != 6 {
			t.Fatalf("%s read back: %v, %d skipped, %+v", format, err, sk, back)
		}
		for i, b := range back {
			want := tags[i]
			if b.Name != want.Name || b.Area != want.Area || b.Offset != want.Offset || b.Bit != want.Bit ||
				b.Type != want.Type || b.Description != want.Description {
				t.Errorf("%s row %d: %+v, want %+v", format, i, b, want)
			}
		}
	}

	// The workbook has TIA Portal's sheet and columns, and Excel-style parts.
	data, _, _, _ := TIAExport(tags, "xlsx")
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		for _, f := range zr.File {
			if f.Name == name {
				rc, _ := f.Open()
				b, _ := io.ReadAll(rc)
				return string(b)
			}
		}
		t.Errorf("%s missing", name)
		return ""
	}
	if !strings.Contains(read("xl/workbook.xml"), `name="PLC Tags"`) {
		t.Error("sheet is not called PLC Tags")
	}
	ss := read("xl/sharedStrings.xml")
	for _, h := range tiaXLSXHead[:8] {
		if !strings.Contains(ss, ">"+h+"<") {
			t.Errorf("column %q missing", h)
		}
	}
	for _, s := range []string{"%I0.3", "%MB0", "%MW21", "%QW64", "%MD100", "UDInt", "Default tag table"} {
		if !strings.Contains(ss, ">"+s+"<") {
			t.Errorf("%q missing", s)
		}
	}
	xmlData, _, _, _ := TIAExport(tags, "xml")
	if !strings.Contains(string(xmlData), `<Tag type='Bool' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='start &lt;&#34;button&#34;&gt; &amp; more' addr='%I0.3'>Start_PB</Tag>`) {
		t.Errorf("xml:\n%s", xmlData)
	}

	if _, _, sk, err := TIAExport(tags[6:], "xlsx"); err == nil || len(sk) != 4 {
		t.Errorf("only unexportable tags: %v %+v", err, sk)
	}
	// A .sdf as the engineering software exports it.
	sdf := "\"SpareQ0.0\",\"%Q0.0\",\"Bool\",\"True\",\"True\",\"False\",\"\",\"\",\"True\"\r\n" +
		"\"Clock_Byte\",\"%MB0\",\"Byte\",\"True\",\"True\",\"False\",\"\",\"\",\"True\"\r\n" +
		"\"Silo_Level_AI\",\"%MW21\",\"Int\",\"True\",\"True\",\"False\",\"level, raw\",\"\",\"True\"\r\n" +
		"\"Fluff01Mi n\",\"%I0.4\",\"Bool\",\"True\",\"True\",\"False\",\"\",\"\",\"True\"\r\n"
	got, sk, err := TIATags("PLCTags.sdf", []byte(sdf))
	if err != nil || sk != 0 || len(got) != 4 {
		t.Fatalf("sdf: %v %d %+v", err, sk, got)
	}
	if got[0] != (Tag{Name: "SpareQ0.0", Area: "Q", Type: "BOOL"}) || got[1] != (Tag{Name: "Clock_Byte", Area: "M", Type: "BYTE"}) ||
		got[2] != (Tag{Name: "Silo_Level_AI", Area: "M", Offset: 21, Type: "INT", Description: "level, raw"}) ||
		got[3] != (Tag{Name: "Fluff01Mi n", Area: "I", Bit: 4, Type: "BOOL"}) {
		t.Errorf("sdf tags: %+v", got)
	}
	if out, _, _, _ := TIAExport(got[:1], "sdf"); string(out) != "\"SpareQ0.0\",\"%Q0.0\",\"Bool\",\"True\",\"True\",\"False\",\"\",\"\",\"True\"\r\n" {
		t.Errorf("sdf export: %q", out)
	}
	if _, _, err := TIATags("x.sdf", []byte("\r\n")); err == nil {
		t.Error("empty sdf accepted")
	}

	if _, _, _, err := TIAExport(tags, "pdf"); err == nil {
		t.Error("pdf accepted")
	}
}
