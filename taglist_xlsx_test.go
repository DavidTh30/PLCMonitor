package main

import "testing"

// Every tag – DB, I/Q/M, Modbus, OPC UA – with every field comes back the
// same after an export and an import.
func TestTagListRoundTrip(t *testing.T) {
	in := []Tag{
		{Name: "Blower01.AutoStart", Area: "DB", DB: 10, Offset: 0, Bit: 0, Type: "BOOL", Description: "Blower01 Auto Start (L1)"},
		{Name: "Step09.Time", Area: "DB", DB: 10, Offset: 78, Type: "UINT", Deadband: 0.5},
		{Name: "Silo.Level", Area: "I", Offset: 64, Type: "INT", Description: "Silo level (%)"},
		{Name: "Lamp", Area: "Q", Offset: 0, Bit: 5, Type: "BOOL"},
		{Name: "Flow", Area: "HR", Offset: 10, Type: "REAL", Unit: 3},
		{Name: "Speed", Area: "UA", Type: "REAL", Node: `ns=3;s="DB1"."Speed"`},
		{Name: "ชื่อไทย", Area: "M", Offset: 4, Type: "DINT"},
	}
	data, n, err := TagListXLSX(in)
	if err != nil || n != len(in) {
		t.Fatal(n, err)
	}
	rows, err := xlsxRows(data)
	if err != nil || rows[1][1] != "DB10.DBX0.0" || rows[0][0] != "Name" {
		t.Fatalf("%q %v", rows[:2], err)
	}
	got, skipped, ok, err := TagListImport("tags.xlsx", data)
	if !ok || err != nil || len(skipped) != 0 || len(got) != len(in) {
		t.Fatal(ok, err, skipped, len(got))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("%d: got %+v\nwant %+v", i, got[i], in[i])
		}
	}
	// A PLC tag table is not a PLC Monitor tag list.
	tia, _, _, _ := TIAExport(in, "xlsx")
	if _, _, ok, _ := TagListImport("PLCTags.xlsx", tia); ok {
		t.Error("PLC tag table read as a tag list")
	}
	// Bad rows say why.
	csv := "name,area,db,offset,bit,type\nA,DB,10,0,0,BOOL\nB,DB,0,2,0,WORD\nC,M,,x,,INT\nD,XX,,0,,INT\n"
	got, skipped, ok, _ = TagListImport("t.csv", []byte(csv))
	if !ok || len(got) != 1 || len(skipped) != 3 || skipped[0].Where != "row 3" {
		t.Fatalf("%v %+v %+v", ok, got, skipped)
	}
}
