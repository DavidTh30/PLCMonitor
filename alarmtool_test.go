package main

import "testing"

func TestAlarmConditions(t *testing.T) {
	for in, want := range map[string]string{"TRUE": "TRUE", "": "TRUE", "false": "FALSE", "bit 3": "bit 3", "X5": "bit 5",
		"> 80": "> 80", ">=12.5": ">= 12.5", "< 0": "< 0", "<> 0": "<> 0", "outside 10..90": "outside 10..90", "inside 20-30": "inside 20..30", "10 to 5": "outside 5..10"} {
		tr, err := parseAlarmCondition(in)
		if err != nil || tr.String() != want {
			t.Errorf("%q → %q, %v; want %q", in, tr.String(), err, want)
		}
	}
	if _, err := parseAlarmCondition("sometimes"); err == nil {
		t.Error("nonsense accepted")
	}
}

// Every export format reads back as the same alarms; a sheet from another
// tool with Bit / Limit columns is understood.
func TestAlarmExportImport(t *testing.T) {
	s := AlarmSet{Name: "Line 1", Alarms: []AlarmDef{
		{ID: 1, Class: "fault", Tag: "Motor_UDT.Fault", Trigger: AlarmTrigger{Type: "true"}, Text: "Motor fault", Ack: true},
		{ID: 2, Class: "warning", Tag: "Tank_Level", Trigger: AlarmTrigger{Type: "gt", Value: 80, DelayMs: 2000}, Text: "Level high: {value} %", Ack: true},
		{ID: 3, Class: "alarm", Group: "Silo", Tag: "Status_Word", Trigger: AlarmTrigger{Type: "bit", Bit: 4}, Text: "Silo door open"},
		{ID: 10, Class: "info", Tag: "Analog_In_1", Trigger: AlarmTrigger{Type: "outside", Min: 100, Max: 27000}, Text: "Sensor out of range"},
	}}
	for _, f := range []string{"plcalarm", "csv", "xlsx"} {
		name, data, err := ExportAlarms(s, f)
		if err != nil {
			t.Fatal(f, err)
		}
		back, err := ImportAlarms(name, data)
		if err != nil || len(back.Alarms) != len(s.Alarms) {
			t.Fatalf("%s: %v %+v", f, err, back)
		}
		for i, a := range back.Alarms {
			w := s.Alarms[i]
			if a.ID != w.ID || a.Class != w.Class || a.Tag != w.Tag || a.Text != w.Text || a.Trigger != w.Trigger || a.Ack != w.Ack || a.Group != w.Group {
				t.Errorf("%s %d: %+v, want %+v", f, i, a, w)
			}
		}
	}
	other := "No.;Message;Trigger tag;Bit;Severity\n5;Door open;Status_Word;3;Warning\n6;Pressure low;P1;;error\n"
	got, err := ImportAlarms("hmi.csv", []byte(other))
	if err != nil || len(got.Alarms) != 2 || got.Alarms[0].Trigger.Type != "bit" || got.Alarms[0].Trigger.Bit != 3 || got.Alarms[0].Class != "warning" ||
		got.Alarms[1].Trigger.Type != "true" || got.Alarms[1].Class != "fault" {
		t.Fatalf("%v %+v", err, got)
	}
	dup := AlarmSet{Alarms: []AlarmDef{{ID: 1, Tag: "a", Text: "x"}, {ID: 1, Tag: "b", Text: "y"}}}
	if err := dup.check(); err == nil {
		t.Error("the same number twice accepted")
	}
}
