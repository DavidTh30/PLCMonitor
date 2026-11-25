package main

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

// The HMIEditERP export written by HMIExport reads back with xlsRows, and
// imports as the same tags.
func TestHMIRoundTrip(t *testing.T) {
	in := []Tag{
		{Name: "aa", Area: "M", Offset: 0, Type: "BYTE"},
		{Name: "hh", Area: "Q", Offset: 0, Bit: 0, Type: "BOOL"},
		{Name: "cc", Area: "I", Offset: 2, Type: "WORD"},
		{Name: "ff", Area: "M", Offset: 4, Type: "DWORD"},
		{Name: "kk", Area: "DB", DB: 10, Offset: 1, Bit: 1, Type: "BOOL"},
		{Name: "uu", Area: "DB", DB: 10, Offset: 78, Type: "WORD"},
		{Name: "gg", Area: "DB", DB: 3, Offset: 4, Type: "DWORD"},
		{Name: "qaz", Area: "DB", DB: 3, Offset: 9, Type: "BYTE"},
		{Name: "ชื่อไทย", Area: "M", Offset: 10, Bit: 7, Type: "BOOL"},
	}
	data, n, _, err := HMIExport(in)
	if err != nil || n != len(in) {
		t.Fatal(n, err)
	}
	rows, err := xlsRows(data)
	if err != nil || len(rows) != len(in)+1 || rows[0][2] != "Address" || rows[1][3] != "0" {
		t.Fatalf("%q %v", rows, err)
	}
	got, skipped, err := TagTableTags("x.xls", data, ProtoS7)
	if err != nil || skipped != 0 || len(got) != len(in) {
		t.Fatal(len(got), skipped, err)
	}
	for i, g := range got {
		w := in[i]
		if g.Name != w.Name || g.Area != w.Area || g.DB != w.DB || g.Offset != w.Offset || g.Bit != w.Bit || g.Type != w.Type {
			t.Errorf("%d: got %+v, want %+v", i, g, w)
		}
	}
}

// HMIEditERP's own example: DBx… addresses need the DB number.
func TestHMIDBx(t *testing.T) {
	rows := "Name,Type,Address,ByteOrder,Extend1,Extend2\n" +
		"gg,D,DBxDBD0_D,0,0,0\nuu,W,DBxDBW0,0,0,0\nqaz,B,DBxDBB0_B,0,0,0\nkk,X,DBxDB0.0,0,0,0\nee,W,MW0,0,0,0\n"
	_, _, err := TagTableTags("x.csv", []byte(rows), ProtoS7)
	var need NeedDBError
	if !errors.As(err, &need) || need.Count != 4 {
		t.Fatalf("%v", err)
	}
	tags, _, err := TagTableTagsDB("x.csv", []byte(rows), ProtoS7, 7)
	if err != nil || len(tags) != 5 {
		t.Fatal(tags, err)
	}
	want := map[string]string{"gg": "DB7 0.0 DWORD", "uu": "DB7 0.0 WORD", "qaz": "DB7 0.0 BYTE", "kk": "DB7 0.0 BOOL", "ee": "M0 0.0 WORD"}
	for _, g := range tags {
		s := fmt.Sprintf("%s%d %d.%d %s", g.Area, g.DB, g.Offset, g.Bit, g.Type)
		if want[g.Name] != s {
			t.Errorf("%s: %s, want %s", g.Name, s, want[g.Name])
		}
	}
}

// HMIEditERP's DB addresses with the DB number: DBxDB + DB (2 digits) + byte (4 digits).
func TestHMIDBNumbered(t *testing.T) {
	rows := "Name,Type,Address,ByteOrder,Extend1,Extend2\n" +
		"LED1,X,DBxDB100000.0,0,0,0\nWord00,W,DBxDBW100000,0,0,0\nLED2,X,DBxDB100000.1,0,0,0\n" +
		"Word01,W,DBxDBW100001,0,0,0\nFlow,D,DBxDBD100004_D,0,0,0\nB,B,DBxDBB030009_B,0,0,0\n"
	tags, _, err := TagTableTags("test.csv", []byte(rows), ProtoS7)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"LED1 DB10 0.0 BOOL", "Word00 DB10 0.0 WORD", "LED2 DB10 0.1 BOOL", "Word01 DB10 1.0 WORD", "Flow DB10 4.0 DWORD", "B DB3 9.0 BYTE"}
	for i, g := range tags {
		if s := fmt.Sprintf("%s %s%d %d.%d %s", g.Name, g.Area, g.DB, g.Offset, g.Bit, g.Type); i >= len(want) || s != want[i] {
			t.Errorf("%d: %s", i, s)
		}
	}
	if len(tags) != len(want) {
		t.Errorf("%d tags", len(tags))
	}
}

// The user's example file from HMIEditERP, when it is there.
func TestHMIExampleFile(t *testing.T) {
	for _, f := range []string{`C:\Users\PC\Desktop\example.xls`, `C:\Users\PC\Desktop\test.xls`} {
		t.Run(f, func(t *testing.T) { hmiExampleFile(t, f) })
	}
}

func hmiExampleFile(t *testing.T, f string) {
	data, err := os.ReadFile(f)
	if err != nil {
		t.Skip(err)
	}
	rows, err := xlsRows(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%q", rows)
	tags, skipped, err := TagTableTagsDB("example.xls", data, ProtoS7, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d tags, %d skipped", len(tags), skipped)
	for _, g := range tags {
		t.Logf("  %s %s DB%d %d.%d %s", g.Name, g.Area, g.DB, g.Offset, g.Bit, g.Type)
	}
}
