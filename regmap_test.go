package main

import "testing"

// A register map of an HMI (Key, Area, Address, Bit, Type, S7Address,
// Description, Enabled), as a .csv: S7 tags from S7Address, or Modbus tags
// from Area / Address / Bit when the network is Modbus.
func TestRegisterMapImport(t *testing.T) {
	csv := "Key,Group,Access,Area,Address,Bit,Type,Scale,Enabled,S7Address,Description\n" +
		"Blower01.AutoStart,Device Status,R,COIL,100,-1,BOOL,1,1,DB10.DBX0.0,Blower01 Auto Start\n" +
		"Blower01.Running,Device Status,R,COIL,109,-1,BOOL,1,1,DB10.DBX1.1,Running\n" +
		"Step09.Time,Pattern,RW,HR,39,-1,UINT16,0.1,1,DB10.DBW78,Step 9 duration (s)\n" +
		"Flow,Values,R,HR,50,-1,FLOAT32,1,1,%DB3.DBD4,Flow\n" +
		"Alarm.Bit3,Values,R,HR,60,3,BOOL,1,1,DB10.DBX90.3,in a register\n" +
		"Spare,Values,R,HR,61,-1,INT16,1,0,DB10.DBW92,disabled row\n" +
		"FluffSilo HMI register map - exported 2026-11-23\n"
	want := map[string][2]Tag{ // S7, Modbus
		"Blower01.AutoStart": {{Area: "DB", DB: 10, Offset: 0, Bit: 0, Type: "BOOL"}, {Area: "COIL", Offset: 100, Type: "BOOL"}},
		"Blower01.Running":   {{Area: "DB", DB: 10, Offset: 1, Bit: 1, Type: "BOOL"}, {Area: "COIL", Offset: 109, Type: "BOOL"}},
		"Step09.Time":        {{Area: "DB", DB: 10, Offset: 78, Type: "UINT"}, {Area: "HR", Offset: 39, Type: "UINT"}},
		"Flow":               {{Area: "DB", DB: 3, Offset: 4, Type: "REAL"}, {Area: "HR", Offset: 50, Type: "REAL"}},
		"Alarm.Bit3":         {{Area: "DB", DB: 10, Offset: 90, Bit: 3, Type: "BOOL"}, {Area: "HR", Offset: 60, Bit: 3, Type: "BOOL"}},
	}
	for i, proto := range []string{ProtoS7, ProtoModbus} {
		tags, skipped, err := TagTableTags("map.csv", []byte(csv), proto)
		if err != nil || len(tags) != len(want) || skipped != 1 {
			t.Fatalf("%s: %d tags, %d skipped, %v", proto, len(tags), skipped, err)
		}
		for _, g := range tags {
			w, ok := want[g.Name]
			if !ok {
				t.Errorf("%s: unexpected %q", proto, g.Name)
				continue
			}
			e := w[i]
			if g.Area != e.Area || g.DB != e.DB || g.Offset != e.Offset || g.Bit != e.Bit || g.Type != e.Type {
				t.Errorf("%s %s: got %s DB%d %d.%d %s, want %s DB%d %d.%d %s", proto, g.Name, g.Area, g.DB, g.Offset, g.Bit, g.Type, e.Area, e.DB, e.Offset, e.Bit, e.Type)
			}
			if err := g.normalize(proto); err != nil {
				t.Errorf("%s %s: %v", proto, g.Name, err)
			}
		}
	}
	// Each row left out says where it is and why; the note line is not one.
	_, sk, err := TagTableImport("map.csv", []byte(csv+"Bad1,X,R,HR,1,-1,BOOL,1,1,DB10.DBX9.9,bad bit\nBad2,X,R,HR,2,-1,STRING,1,1,DB10.DBW94,a string\n"), ProtoS7, 0)
	if err != nil || len(sk) != 3 {
		t.Fatalf("%+v %v", sk, err)
	}
	for i, w := range []SkippedRow{{"row 7", "Spare", "DB10.DBW92", "turned off in the file (Enabled = 0)"}, {"row 9", "Bad1", "DB10.DBX9.9", ""}, {"row 10", "Bad2", "DB10.DBW94", ""}} {
		if sk[i].Where != w.Where || sk[i].Name != w.Name || sk[i].Address != w.Address || w.Reason != "" && sk[i].Reason != w.Reason || sk[i].Reason == "" {
			t.Errorf("%d: %+v, want %+v", i, sk[i], w)
		}
	}
	// TIATags reads the PLC (S7) addresses.
	if tags, _, err := TIATags("map.csv", []byte(csv)); err != nil || len(tags) != len(want) || tags[0].Area != "DB" {
		t.Errorf("TIATags: %d tags, %v", len(tags), err)
	}
}
