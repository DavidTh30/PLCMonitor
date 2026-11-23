package main

import (
	"strings"
	"testing"
)

func TestSourceTags(t *testing.T) {
	// fbSource (symbols_test.go): FB_Top_DB (82 bytes), a timer DB (16), …
	found := []PLCArea{{Area: "DB", DB: 5, Size: 82}, {Area: "DB", DB: 6, Size: 16}, {Area: "DB", DB: 7, Size: 16}}
	blocks, err := SourceTags("fb.scl", []byte(fbSource), map[string]int{"Opt_DB": 9}, found)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SourceBlock{}
	for _, b := range blocks {
		by[b.Name] = b
	}
	top := by["FB_Top_DB"]
	if top.DB != 5 || top.Size != 82 { // the only DB of that size
		t.Errorf("FB_Top_DB: DB%d size %d", top.DB, top.Size)
	}
	if tm := by["IEC_Timer_0_DB"]; tm.DB != 0 { // two DBs of 16 bytes: not guessed
		t.Errorf("timer DB guessed as DB%d", tm.DB)
	}
	if by["Opt_DB"].DB != 9 || !by["Opt_DB"].Optimized {
		t.Errorf("Opt_DB: %+v", by["Opt_DB"])
	}
	var run, ref Tag
	for _, tg := range top.Tags {
		switch tg.Name {
		case "FB_Top_DB.M1.Run":
			run = tg
		case "FB_Top_DB.M1.Ref":
			ref = tg
		}
	}
	if run != (Tag{Name: "FB_Top_DB.M1.Run", Area: "DB", DB: 5, Offset: 10, Type: "BOOL"}) {
		t.Errorf("M1.Run: %+v", run)
	}
	if ref.Name != "" || top.Other == 0 { // the in/out pointer can't be a tag
		t.Errorf("pointer as tag: %+v, other %d", ref, top.Other)
	}
	if _, err := SourceTags("x.xlsx", nil, nil, nil); err == nil {
		t.Error("xlsx accepted as source")
	}
	if _, err := SourceTags("x.scl", []byte("FUNCTION \"F\" : Void\nBEGIN\nEND_FUNCTION\n"), nil, nil); err == nil {
		t.Error("source without data blocks accepted")
	}
}

func TestTagsToSCL(t *testing.T) {
	tags := []Tag{
		{Name: "Line.Start", Area: "DB", DB: 10, Offset: 0, Bit: 0, Type: "BOOL", Description: "start\nbutton"},
		{Name: "Line.Stop", Area: "DB", DB: 10, Offset: 0, Bit: 3, Type: "BOOL"},
		{Name: "Line.Lamp", Area: "DB", DB: 10, Offset: 2, Bit: 6, Type: "BOOL"},
		{Name: "Line.Next", Area: "DB", DB: 10, Offset: 1, Bit: 0, Type: "BOOL"}, // the byte right after a Bool
		{Name: "Line.Mode", Area: "DB", DB: 10, Offset: 3, Type: "BYTE"},
		{Name: "Line.Speed", Area: "DB", DB: 10, Offset: 4, Type: "INT"},
		{Name: "Line.Total", Area: "DB", DB: 10, Offset: 20, Type: "REAL"},
		{Name: "Line.Odd", Area: "DB", DB: 10, Offset: 25, Type: "INT"},          // odd byte: left out
		{Name: "Line.Over", Area: "DB", DB: 10, Offset: 21, Type: "BYTE"},        // inside Total: left out
		{Name: "Line.Tail", Area: "DB", DB: 10, Offset: 33, Type: "BYTE"},        // odd byte after a gap
		{Name: "Dev[0].Run", Area: "DB", DB: 2, Offset: 6, Bit: 1, Type: "BOOL"}, // name with . and [ ]
		{Name: "Spare_0", Area: "DB", DB: 2, Offset: 7, Type: "BYTE"},            // clashes with a filler name
		{Name: "Lamp", Area: "Q", Offset: 0, Bit: 1, Type: "BOOL"},               // not a DB
		{Name: "Precise", Area: "DB", DB: 2, Offset: 8, Type: "LREAL"},
	}
	src, n, skipped, err := TagsToSCL(tags, map[int]string{10: "Line"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 || len(skipped) != 3 {
		t.Errorf("%d written, skipped %+v", n, skipped)
	}
	s := string(src)
	for _, want := range []string{`DATA_BLOCK "Line"`, `DATA_BLOCK "DB2"`, `"Start" : Bool;   // start button`, `"Dev_0_Run" : Bool;`,
		`{ S7_Optimized_Access := 'FALSE' }`, `"Precise" : LReal;`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	// Read back: every written tag at its address.
	blocks, err := SourceTags("tags.scl", src, map[string]int{"Line": 10, "DB2": 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := map[string][2]int{}
	for _, b := range blocks {
		for _, tg := range b.Tags {
			at[tg.Name] = [2]int{tg.DB*1000 + tg.Offset, tg.Bit}
		}
	}
	check := func(name string, db, off, bit int) {
		t.Helper()
		if got, ok := at[name]; !ok || got != [2]int{db*1000 + off, bit} {
			t.Errorf("%s: %v, want DB%d.%d.%d", name, got, db, off, bit)
		}
	}
	check("Line.Start", 10, 0, 0)
	check("Line.Stop", 10, 0, 3)
	check("Line.Next", 10, 1, 0)
	check("Line.Lamp", 10, 2, 6)
	check("Line.Mode", 10, 3, 0)
	check("Line.Speed", 10, 4, 0)
	check("Line.Total", 10, 20, 0)
	check("Line.Tail", 10, 33, 0)
	check("DB2.Dev_0_Run", 2, 6, 1)
	check("DB2.Precise", 2, 8, 0)
	if _, ok := at["DB2.Spare_0"]; !ok {
		t.Error("tag Spare_0 lost its name to a filler")
	}

	if _, _, _, err := TagsToSCL([]Tag{{Name: "x", Area: "M", Type: "INT"}}, nil); err == nil {
		t.Error("no DB tags: no error")
	}
}

// A DB read from a source, written back from its tags and read again,
// keeps every address.
func TestSCLRoundTrip(t *testing.T) {
	blocks, err := SourceTags("fb.scl", []byte(fbSource), map[string]int{"FB_Top_DB": 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var tags []Tag
	for _, b := range blocks {
		if b.Name == "FB_Top_DB" {
			tags = b.Tags
		}
	}
	src, n, skipped, err := TagsToSCL(tags, map[int]string{3: "FB_Top_DB"})
	if err != nil || n != len(tags) || len(skipped) != 0 {
		t.Fatalf("%v: %d of %d, skipped %+v", err, n, len(tags), skipped)
	}
	back, err := SourceTags("back.scl", src, map[string]int{"FB_Top_DB": 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[[2]int]bool{}
	for _, tg := range back[0].Tags {
		pos[[2]int{tg.Offset, tg.Bit}] = true
	}
	for _, tg := range tags {
		if !pos[[2]int{tg.Offset, tg.Bit}] {
			t.Errorf("%s (%d.%d) moved", tg.Name, tg.Offset, tg.Bit)
		}
	}
}
