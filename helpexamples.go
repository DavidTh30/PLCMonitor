package main

// Example files for the Help page: spreadsheets (.xlsx / .csv) and SCL
// sources for general variables (I, Q, M), a data block, and a UDT used as
// an array. The spreadsheet and the SCL of the data block describe the same
// block, and so do the two UDT examples (checked in helpexamples_test.go).

import (
	"bytes"
	"encoding/csv"
	"errors"
	"net/http"
	"strings"
)

// HelpExample is one example: a table (Head, Rows) or a source text.
type HelpExample struct {
	ID    string     `json:"id"`
	Kind  string     `json:"kind"` // table or scl
	File  string     `json:"file"` // name to save as, without the extension for tables
	Head  []string   `json:"head,omitempty"`
	Rows  [][]string `json:"rows,omitempty"`
	Text  string     `json:"text,omitempty"`
	Tags  int        `json:"tags"` // tags it gives when imported
	Usage string     `json:"usage"`
}

var helpExamples = []HelpExample{
	{ID: "x-iqm", Kind: "table", File: "Example_PLC_tags", Tags: 9,
		Usage: "File → Import → PLC tag table … (or drop the file on the window)",
		Head:  []string{"Name", "Path", "Data Type", "Logical Address", "Comment"},
		Rows: [][]string{
			{"Start_PB", "Default tag table", "Bool", "%I0.0", "Start push button (NO)"},
			{"Stop_PB", "Default tag table", "Bool", "%I0.1", "Stop push button (NC)"},
			{"Level_AI", "Default tag table", "Int", "%IW64", "Level sensor 0..27648"},
			{"Motor_Run", "Default tag table", "Bool", "%Q0.0", "Motor contactor"},
			{"Lamp_Fault", "Default tag table", "Bool", "%Q0.1", "Fault lamp"},
			{"Speed_AO", "Default tag table", "Int", "%QW64", "Speed setpoint to the drive"},
			{"Auto_Mode", "Default tag table", "Bool", "%M10.0", "Auto mode on"},
			{"Part_Count", "Default tag table", "DInt", "%MD20", "Parts counter"},
			{"Flow", "Default tag table", "Real", "%MD24", "Flow (l/min)"},
		}},
	{ID: "x-db", Kind: "table", File: "Example_DB_Silo", Tags: 8,
		Usage: "File → Import → PLC tag table / register map … – the same block as the SCL example “Data block”",
		Head:  []string{"Name", "Data Type", "Address", "Comment"},
		Rows: [][]string{
			{"DB_Silo.Blower.Run", "Bool", "DB10.DBX0.0", "Blower contactor on"},
			{"DB_Silo.Blower.Fault", "Bool", "DB10.DBX0.1", "Blower fault"},
			{"DB_Silo.Blower.Hours", "DInt", "DB10.DBD2", "Running hours"},
			{"DB_Silo.Level", "Real", "DB10.DBD6", "Silo level (%)"},
			{"DB_Silo.Setpoint", "Int", "DB10.DBW10", "Level setpoint (%)"},
			{"DB_Silo.Temps[0]", "Real", "DB10.DBD12", "Temperature 1 (°C)"},
			{"DB_Silo.Temps[1]", "Real", "DB10.DBD16", "Temperature 2 (°C)"},
			{"DB_Silo.Temps[2]", "Real", "DB10.DBD20", "Temperature 3 (°C)"},
		}},
	{ID: "x-udt", Kind: "table", File: "Motor_UDT", Tags: 13,
		Usage: "Drop the file on the window: DB 20, start byte 0, tag prefix Motor, Array 3 from 1 → Motor[1].Run … Motor[3].Alarms[7] (3 × 13 tags, 14 bytes each)",
		Head:  []string{"Name", "Data Type", "Comment"},
		Rows: [][]string{
			{"Run", "Bool", "Running"},
			{"Fault", "Bool", "Fault"},
			{"Speed", "Int", "Speed (rpm)"},
			{"Current", "Real", "Current (A)"},
			{"Hours", "DInt", "Running hours"},
			{"Alarms", "Array[0..7] of Bool", "Alarm bits"},
		}},
	{ID: "s-vars", Kind: "scl", File: "DB_Values.scl", Tags: 8,
		Usage: "File → Import → Data blocks from SCL source … – also: PLC engineering software → External source files → Add → Generate blocks from source",
		Text: `DATA_BLOCK "DB_Values"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
NON_RETAIN
   STRUCT
      Start : Bool;   // start command
      Stop : Bool;   // stop command
      Running : Bool;   // running feedback
      Mode : Byte;   // 0 = off, 1 = manual, 2 = auto
      Speed : Int;   // rpm
      Status : Word;   // status bits
      Setpoint : Real;   // speed setpoint (rpm)
      Counter : DInt;   // parts counter
   END_STRUCT;

BEGIN
   Setpoint := 1500.0;
END_DATA_BLOCK
`},
	{ID: "s-db", Kind: "scl", File: "DB_Silo.scl", Tags: 8,
		Usage: "File → Import → Data blocks from SCL source …, DB number 10 – the same addresses as the spreadsheet example “Data block”",
		Text: `DATA_BLOCK "DB_Silo"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
NON_RETAIN
   STRUCT
      Blower : Struct
         Run : Bool;   // Blower contactor on
         Fault : Bool;   // Blower fault
         Hours : DInt;   // Running hours
      END_STRUCT;
      Level : Real;   // Silo level (%)
      Setpoint : Int;   // Level setpoint (%)
      Temps : Array[0..2] of Real;   // Temperatures (°C)
   END_STRUCT;

BEGIN
END_DATA_BLOCK
`},
	{ID: "s-udt", Kind: "scl", File: "DB_Motors.scl", Tags: 39,
		Usage: "File → Import → Data blocks from SCL source …, DB number 20 → DB_Motors.Motor[1].Run … Motor[3].Alarms[7] – the same layout as the spreadsheet example “UDT + Array”",
		Text: `TYPE "Motor_UDT"
VERSION : 0.1
   STRUCT
      Run : Bool;   // Running
      Fault : Bool;   // Fault
      Speed : Int;   // Speed (rpm)
      Current : Real;   // Current (A)
      Hours : DInt;   // Running hours
      Alarms : Array[0..7] of Bool;   // Alarm bits
   END_STRUCT;
END_TYPE

DATA_BLOCK "DB_Motors"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
NON_RETAIN
   STRUCT
      Motor : Array[1..3] of "Motor_UDT";
   END_STRUCT;

BEGIN
END_DATA_BLOCK
`},
}

func helpExample(id string) (HelpExample, bool) {
	for _, e := range helpExamples {
		if e.ID == id {
			return e, true
		}
	}
	return HelpExample{}, false
}

// helpExampleFile is an example as a file: format xlsx or csv for tables,
// scl for sources.
func helpExampleFile(id, format string) (name string, data []byte, err error) {
	e, ok := helpExample(id)
	if !ok {
		return "", nil, errors.New("no such example")
	}
	if e.Kind == "scl" {
		return e.File, []byte(strings.ReplaceAll(e.Text, "\n", "\r\n")), nil
	}
	switch format {
	case "csv":
		var b bytes.Buffer
		b.WriteString("\xEF\xBB\xBF") // BOM: Excel reads it as UTF-8
		w := csv.NewWriter(&b)
		w.UseCRLF = true
		w.Write(e.Head)
		w.WriteAll(e.Rows)
		return e.File + ".csv", b.Bytes(), w.Error()
	case "xlsx", "":
		rows := make([][]any, len(e.Rows))
		for i, r := range e.Rows {
			rows[i] = make([]any, len(r))
			for j, c := range r {
				rows[i][j] = c
			}
		}
		widths := make([]float64, len(e.Head))
		for i := range widths {
			widths[i] = 14
			for _, r := range e.Rows {
				if i < len(r) {
					widths[i] = max(widths[i], float64(len([]rune(r[i])))+2)
				}
			}
		}
		sheet := "Tags"
		if e.Head[0] == "Name" && len(e.Head) > 3 && e.Head[3] == "Logical Address" {
			sheet = "PLC Tags"
		}
		data, err := xlsxWorkbook(sheet, e.Head, rows, widths)
		return e.File + ".xlsx", data, err
	}
	return "", nil, errors.New("format must be xlsx or csv")
}

// helpRoutes: GET /api/help/examples, GET /api/help/example?id=…&format=xlsx|csv.
func helpRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/help/examples", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, helpExamples)
	})
	mux.HandleFunc("GET /api/help/example", func(w http.ResponseWriter, r *http.Request) {
		name, data, err := helpExampleFile(r.URL.Query().Get("id"), r.URL.Query().Get("format"))
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "data": data})
	})
}
