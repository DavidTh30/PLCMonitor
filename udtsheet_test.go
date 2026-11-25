package main

import (
	"fmt"
	"strings"
	"testing"
)

func udtTags(t *testing.T, name, csv string) SourceBlock {
	t.Helper()
	blocks, ok, err := UDTSheet(name, []byte(csv))
	if !ok || err != nil || len(blocks) != 1 {
		t.Fatalf("ok %v, err %v, %d blocks", ok, err, len(blocks))
	}
	return blocks[0]
}

func tagLine(g Tag) string { return fmt.Sprintf("%s %d.%d %s", g.Name, g.Offset, g.Bit, g.Type) }

// Laid out by the standard-access rules when there is no Offset column.
func TestUDTSheetLayout(t *testing.T) {
	csv := "Name,Data Type,Comment\n" +
		"Motor,Struct,\nMotor.Run,Bool,running\nMotor.Fault,Bool,\nMotor.Speed,Int,rpm\n" +
		"Level,Real,\nValues,Array[0..1] of Int,\nMy Tag,Word,with a space\nText,String[10],\n"
	b := udtTags(t, "Silo_UDT.csv", csv)
	want := []string{"Silo_UDT.Motor.Run 0.0 BOOL", "Silo_UDT.Motor.Fault 0.1 BOOL", "Silo_UDT.Motor.Speed 2.0 INT",
		"Silo_UDT.Level 4.0 REAL", "Silo_UDT.Values[0] 8.0 INT", "Silo_UDT.Values[1] 10.0 INT", "Silo_UDT.My Tag 12.0 WORD"}
	if len(b.Tags) != len(want) {
		t.Fatalf("%d tags: %v", len(b.Tags), b.Tags)
	}
	for i, g := range b.Tags {
		if tagLine(g) != want[i] {
			t.Errorf("%d: %s, want %s", i, tagLine(g), want[i])
		}
	}
	if b.Name != "Silo_UDT" || b.Size != 26 || b.Other != 1 || b.Tags[2].Description != "rpm" {
		t.Errorf("block %s size %d other %d desc %q", b.Name, b.Size, b.Other, b.Tags[2].Description)
	}
}

// With an Offset column the offsets are taken as written.
func TestUDTSheetOffsets(t *testing.T) {
	csv := "Name;Data Type;Offset;Comment\nA.Run;Bool;0.0;\nA.Stop;Bool;0.3;\nA.Speed;UINT16;2;\nB.Flow;Real;4.0;\n"
	b := udtTags(t, "x.csv", csv)
	want := []string{"x.A.Run 0.0 BOOL", "x.A.Stop 0.3 BOOL", "x.A.Speed 2.0 UINT", "x.B.Flow 4.0 REAL"}
	for i, g := range b.Tags {
		if i >= len(want) || tagLine(g) != want[i] {
			t.Errorf("%d: %s", i, tagLine(g))
		}
	}
	if b.Size != 8 {
		t.Errorf("size %d", b.Size)
	}
}

// Save as SCL: the UDT and a data block holding it at a start byte, as an
// array – read back, every member has the address the window showed.
func TestUDTSaveAsSCL(t *testing.T) {
	_, data, err := helpExampleFile("x-udt", "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	blocks, ok, err := UDTSheet("Motor_UDT.xlsx", data)
	if !ok || err != nil || blocks[0].UDT == "" {
		t.Fatal(ok, err)
	}
	u := blocks[0]
	stride := u.Size + u.Size%2
	for _, c := range []struct{ start, count, from int }{{0, 1, 0}, {10, 3, 1}, {100, 2, 0}} {
		src, err := UDTDataBlockSCL(u.UDT, u.Name, "DB20_Motor", "Motor", c.start, c.count, c.from)
		if err != nil {
			t.Fatal(err)
		}
		back, err := SourceTags("x.scl", []byte(src), nil, nil)
		if err != nil || len(back) != 1 {
			t.Fatalf("%+v: %v\n%s", c, err, src)
		}
		var got []Tag // without the spare bytes before the UDT
		for _, g := range back[0].Tags {
			if !strings.HasPrefix(g.Name, "DB20_Motor.Spare_0") {
				got = append(got, g)
			}
		}
		if len(got) != len(u.Tags)*c.count || len(back[0].Tags)-len(got) != c.start {
			t.Fatalf("%+v: %d tags (%d spare), want %d", c, len(got), len(back[0].Tags)-len(got), len(u.Tags)*c.count)
		}
		for k := 0; k < c.count; k++ {
			for i, m := range u.Tags {
				g := got[k*len(u.Tags)+i]
				rel := m.Name[len(u.Name)+1:]
				name := "DB20_Motor.Motor." + rel
				if c.count > 1 {
					name = fmt.Sprintf("DB20_Motor.Motor[%d].%s", c.from+k, rel)
				}
				if g.Name != name || g.Offset != m.Offset+c.start+k*stride || g.Bit != m.Bit || g.Type != m.Type {
					t.Errorf("%+v: %s %d.%d, want %s %d.%d", c, g.Name, g.Offset, g.Bit, name, m.Offset+c.start+k*stride, m.Bit)
				}
			}
		}
	}
	if _, err := UDTDataBlockSCL(u.UDT, u.Name, "", "", 3, 1, 0); err == nil {
		t.Error("odd start byte accepted")
	}
}

// Tag tables and tag lists are not UDT sheets.
func TestUDTSheetNot(t *testing.T) {
	for _, csv := range []string{
		"Name,Data Type,Logical Address\nA,Bool,%M0.0\n",
		"Key,Type,S7Address\nA,BOOL,DB10.DBX0.0\n",
		"name,area,db,offset,bit,type\nA,DB,1,0,0,BOOL\n",
		"Name,Type,Address,ByteOrder,Extend1,Extend2\naa,B,MB0_B,0,0,0\n",
	} {
		if _, ok, _ := UDTSheet("t.csv", []byte(csv)); ok {
			t.Errorf("read as a UDT: %q", csv)
		}
	}
}
