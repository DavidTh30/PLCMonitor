package main

import (
	"strings"
	"testing"
)

func TestLayoutUDT(t *testing.T) {
	ms := []UDTMember{
		{Name: "Run", Type: "Bool"}, {Name: "Fault", Type: "Bool"}, {Name: "Speed", Type: "Int", Comment: "rpm"},
		{Name: "Drive", Type: "Struct"}, {Name: "Drive.Current", Type: "Real"}, {Name: "Drive.Ok", Type: "Bool"},
		{Name: "Alarms", Type: "Array[0..7] of Bool"}, {Name: "Text", Type: "String[10]"},
	}
	lay, err := LayoutUDT("Motor UDT", ms)
	if err != nil {
		t.Fatal(err)
	}
	off := map[string]string{}
	for _, m := range lay.Members {
		off[m.Name] = m.Offset
	}
	want := map[string]string{"Run": "0.0", "Fault": "0.1", "Speed": "2.0", "Drive": "4.0", "Drive.Current": "4.0", "Drive.Ok": "8.0", "Alarms": "10.0", "Text": ""}
	for k, v := range want {
		if off[k] != v {
			t.Errorf("%s at %q, want %q", k, off[k], v)
		}
	}
	if lay.Name != "Motor_UDT" || lay.Size != 24 || lay.Other != 1 || !strings.HasPrefix(lay.SCL, `TYPE "Motor_UDT"`) {
		t.Errorf("%s size %d other %d\n%s", lay.Name, lay.Size, lay.Other, lay.SCL)
	}
	// A lone array element from a tag: written Alarms_4, its offset still found.
	one, err := LayoutUDT("x", []UDTMember{{Name: "Run", Type: "Bool"}, {Name: "Alarms[4]", Type: "Bool"}, {Name: "G.V[2]", Type: "Int"}})
	if err != nil || !strings.Contains(one.SCL, "Alarms_4 : Bool;") || !strings.Contains(one.SCL, "V_2 : Int;") ||
		one.Members[1].Offset != "0.1" || one.Members[2].Offset != "2.0" {
		t.Errorf("%v %+v\n%s", err, one.Members, one.SCL)
	}
	if _, err := LayoutUDT("x", []UDTMember{{Name: "A..B", Type: "Int"}}); err == nil {
		t.Error("empty name part accepted")
	}
	if _, err := LayoutUDT("x", []UDTMember{{Name: "A", Type: "Nonsense"}}); err == nil {
		t.Error("unknown type accepted")
	}
}

// A UDT exported as SCL imports back with the same members and layout, and a
// source's TYPE and DATA_BLOCK can be imported (a nested UDT written out).
func TestUDTExportImport(t *testing.T) {
	ms := []UDTMember{{Name: "Run", Type: "Bool", Comment: "running"}, {Name: "Speed", Type: "Int"},
		{Name: "Pid", Type: "Struct"}, {Name: "Pid.Kp", Type: "Real"}, {Name: "Values", Type: "Array[1..3] of DInt"}}
	for _, f := range []string{"scl", "xlsx", "csv"} {
		file, data, err := ExportUDT("Pump_UDT", ms, f)
		if err != nil {
			t.Fatal(f, err)
		}
		c, err := ImportUDT(file, data)
		if err != nil || len(c) != 1 {
			t.Fatalf("%s: %v %+v", file, err, c)
		}
		if c[0].Name != "Pump_UDT" || len(c[0].Members) != len(ms) {
			t.Fatalf("%s: %+v", file, c[0])
		}
		for i, m := range c[0].Members {
			if m.Name != ms[i].Name || !strings.EqualFold(m.Type, ms[i].Type) || m.Comment != ms[i].Comment {
				t.Errorf("%s %d: %+v, want %+v", file, i, m, ms[i])
			}
		}
	}
	// The Help's DB_Motors source: the UDT and nothing else (the DB is an array of it).
	e, _ := helpExample("s-udt")
	c, err := ImportUDT(e.File, []byte(e.Text))
	if err != nil || len(c) != 2 || c[0].Name != "Motor_UDT" || len(c[0].Members) != 6 {
		t.Fatalf("%v %+v", err, c)
	}
	// The DB holds Array[1..3] of "Motor_UDT": kept as written.
	if c[1].Kind != "db" || c[1].Members[0].Type != `Array[1..3] of "Motor_UDT"` {
		t.Errorf("%+v", c[1])
	}
	lay, err := LayoutUDT("M", c[0].Members)
	if err != nil || lay.Size != 14 {
		t.Errorf("Motor_UDT size %d %v", lay.Size, err)
	}
}
