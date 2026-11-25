package main

import (
	"bytes"
	"testing"
)

func TestHMIAddress(t *testing.T) {
	for _, c := range []struct {
		tag       Tag
		typ, addr string
	}{
		{Tag{Area: "M", Offset: 0, Type: "BYTE"}, "B", "MB0_B"},
		{Tag{Area: "Q", Offset: 0, Bit: 0, Type: "BOOL"}, "X", "Q0.0"},
		{Tag{Area: "I", Offset: 0, Type: "WORD"}, "W", "IW0"},
		{Tag{Area: "Q", Offset: 0, Type: "INT"}, "W", "QW0"},
		{Tag{Area: "M", Offset: 0, Type: "DINT"}, "D", "MD0_D"},
		{Tag{Area: "DB", DB: 10, Offset: 0, Bit: 0, Type: "BOOL"}, "X", "DBxDB100000.0"},
		{Tag{Area: "DB", DB: 10, Offset: 1, Bit: 2, Type: "BOOL"}, "X", "DBxDB100001.2"},
		{Tag{Area: "DB", DB: 10, Offset: 1, Type: "WORD"}, "W", "DBxDBW100001"},
		{Tag{Area: "DB", DB: 10, Offset: 1, Type: "DWORD"}, "D", "DBxDBD100001_D"},
		{Tag{Area: "DB", DB: 10, Offset: 4, Type: "REAL"}, "D", "DBxDBD100004_D"},
		{Tag{Area: "DB", DB: 10, Offset: 78, Type: "UINT"}, "W", "DBxDBW100078"},
		{Tag{Area: "DB", DB: 3, Offset: 0, Type: "USINT"}, "B", "DBxDBB030000_B"},
		{Tag{Area: "DB", DB: 120, Offset: 1234, Type: "INT"}, "W", "DBxDBW1201234"},
		{Tag{Area: "I", Offset: 0, Bit: 0, Type: "BOOL"}, "X", "I0.0"},
	} {
		typ, addr, why := hmiAddress(c.tag)
		if typ != c.typ || addr != c.addr || why != "" {
			t.Errorf("%+v: %s %s %q, want %s %s", c.tag, typ, addr, why, c.typ, c.addr)
		}
	}
	for _, bad := range []Tag{{Area: "HR", Type: "WORD"}, {Area: "UA", Type: "REAL"}, {Area: "DB", DB: 1, Type: "LREAL"}, {Area: "DB", DB: 1, Offset: 10000, Type: "WORD"}} {
		if _, _, why := hmiAddress(bad); why == "" {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestHMIExportXLS(t *testing.T) {
	tags := []Tag{
		{Name: "aa", Area: "M", Offset: 0, Type: "BYTE"},
		{Name: "Motor ความเร็ว", Area: "DB", DB: 10, Offset: 4, Type: "REAL"},
		{Name: "mb", Area: "HR", Offset: 1, Type: "WORD"},
	}
	data, n, skipped, err := HMIExport(tags)
	if err != nil || n != 2 || len(skipped) != 1 {
		t.Fatal(n, skipped, err)
	}
	if !bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}) || len(data)%512 != 0 {
		t.Fatal("not a compound file")
	}
	// The workbook stream starts at sector 0 with a BIFF8 BOF.
	if !bytes.Equal(data[512:516], []byte{0x09, 0x08, 0x10, 0x00}) {
		t.Errorf("no BOF: % x", data[512:520])
	}
	for _, s := range []string{"DSO_DX", "MB0_B", "DBxDBD100004_D", "ByteOrder"} {
		if !bytes.Contains(data, []byte(s)) {
			t.Errorf("%s not in the file", s)
		}
	}
	if _, _, _, err := HMIExport([]Tag{{Name: "x", Area: "HR", Type: "WORD"}}); err == nil {
		t.Error("no error without ISO-TCP tags")
	}
}
