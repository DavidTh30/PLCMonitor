package main

import (
	"fmt"
	"strings"
	"testing"
)

// Every Help example imports as it says, and the spreadsheet and SCL
// versions of the data block and of the UDT give the same addresses.
func TestHelpExamples(t *testing.T) {
	addr := func(g Tag) string { return fmt.Sprintf("DB%d %d.%d %s", g.DB, g.Offset, g.Bit, g.Type) }
	got := map[string][]Tag{}
	for _, e := range helpExamples {
		if e.Kind == "table" {
			for _, f := range []string{"xlsx", "csv"} {
				name, data, err := helpExampleFile(e.ID, f)
				if err != nil {
					t.Fatalf("%s %s: %v", e.ID, f, err)
				}
				var tags []Tag
				if blocks, ok, err := UDTSheet(name, data); ok {
					if err != nil || len(blocks) != 1 {
						t.Fatalf("%s: %v", name, err)
					}
					tags = blocks[0].Tags
				} else {
					var sk []SkippedRow
					tags, sk, err = TagTableImport(name, data, ProtoS7, 0)
					if err != nil || len(sk) > 0 {
						t.Fatalf("%s: %v %+v", name, err, sk)
					}
				}
				if len(tags) != e.Tags {
					t.Errorf("%s: %d tags, the Help says %d", name, len(tags), e.Tags)
				}
				got[e.ID] = tags
			}
			continue
		}
		blocks, err := SourceTags(e.File, []byte(e.Text), nil, nil)
		if err != nil || len(blocks) != 1 {
			t.Fatalf("%s: %v", e.File, err)
		}
		if n := len(blocks[0].Tags); n != e.Tags {
			t.Errorf("%s: %d tags, the Help says %d", e.File, n, e.Tags)
		}
		got[e.ID] = blocks[0].Tags
	}
	// Data block: the spreadsheet (DB10) = the SCL source given DB 10.
	x, s := got["x-db"], got["s-db"]
	for i := range x {
		s[i].DB = 10
		if x[i].Name != s[i].Name || addr(x[i]) != addr(s[i]) {
			t.Errorf("data block %d: sheet %s %s, SCL %s %s", i, x[i].Name, addr(x[i]), s[i].Name, addr(s[i]))
		}
	}
	// UDT: the sheet's layout = Motor[1] of the SCL array; Motor[2] starts 14 bytes on.
	u, m := got["x-udt"], got["s-udt"]
	for i := range u {
		rel := strings.TrimPrefix(u[i].Name, "Motor_UDT.")
		if m[i].Name != "DB_Motors.Motor[1]."+rel || m[i].Offset != u[i].Offset || m[i].Bit != u[i].Bit || m[i].Type != u[i].Type {
			t.Errorf("UDT %d: sheet %s %d.%d, SCL %s %d.%d", i, u[i].Name, u[i].Offset, u[i].Bit, m[i].Name, m[i].Offset, m[i].Bit)
		}
		if n := m[i+len(u)]; n.Offset != u[i].Offset+14 || !strings.HasPrefix(n.Name, "DB_Motors.Motor[2].") {
			t.Errorf("Motor[2] %d: %s at %d", i, n.Name, n.Offset)
		}
	}
}
