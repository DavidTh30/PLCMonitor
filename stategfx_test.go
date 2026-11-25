package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func svgURL(s string) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(s))
}

func TestStateExportImport(t *testing.T) {
	set := StateSet{Name: "Pump states", Graphics: []StateGraphic{{
		ID: "g1", Name: "Pump 1", Tag: "Motor_UDT.Run", Width: 64, Height: 64,
		States: []StateImage{
			{Label: "Off", When: StateWhen{Op: "eq", Value: 0}, Image: svgURL(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
			{Label: "On", When: StateWhen{Op: "eq", Value: 1}, Image: svgURL(`<svg xmlns="http://www.w3.org/2000/svg"><circle r="5"/></svg>`)},
			{Label: "Fault", When: StateWhen{Op: "else"}, Blink: true},
		},
	}}}
	data, err := ExportStateZIP(set)
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, n := range []string{"Pump_states.plcstate", "states.csv", "images/01_Pump_1/01_Off.svg", "images/01_Pump_1/02_On.svg"} {
		if !names[n] {
			t.Errorf("%s not in the ZIP: %v", n, names)
		}
	}
	back, err := ImportStateFile("x.zip", data)
	if err != nil || len(back.Graphics) != 1 || len(back.Graphics[0].States) != 3 || back.Graphics[0].States[2].When.Op != "else" || !back.Graphics[0].States[2].Blink {
		t.Fatalf("%v %+v", err, back)
	}
	// A ZIP of images only: a graphic per folder, motor_0 / motor_1 → values 0 / 1.
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, n := range []string{"Motor/motor_1.svg", "Motor/motor_0.svg", "Valve/open.png"} {
		w, _ := zw.Create(n)
		w.Write([]byte("<svg/>"))
	}
	zw.Close()
	imgs, err := ImportStateFile("pics.zip", b.Bytes())
	if err != nil || len(imgs.Graphics) != 2 || imgs.Graphics[0].Name != "Motor" || imgs.Graphics[0].States[1].When.Value != 1 ||
		!strings.HasPrefix(imgs.Graphics[1].States[0].Image, "data:image/png;base64,") {
		t.Fatalf("%v %+v", err, imgs)
	}
	// Bad files are refused.
	for _, bad := range []string{`{"format":"other"}`, `{"graphics":[{"states":[{"when":{"op":"less"}}]}]}`, `{"graphics":[{"states":[{"when":{"op":"eq"},"image":"javascript:x"}]}]}`} {
		if _, err := ImportStateFile("x.plcstate", []byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// Mode colours are kept in the file and must be #rrggbb.
func TestStateModeColors(t *testing.T) {
	s := StateSet{ModeColors: map[string]string{"manual": "#ff8800"}, ModeNoBadge: true}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(s)
	var back StateSet
	if err := json.Unmarshal(data, &back); err != nil || back.ModeColors["manual"] != "#ff8800" || !back.ModeNoBadge {
		t.Fatalf("%v %+v", err, back)
	}
	s.ModeColors["auto"] = "red"
	if err := s.check(); err == nil {
		t.Error("a colour name accepted")
	}
}
