package main

import (
	"archive/zip"
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const tiaSource = `TYPE "Motor_T"
VERSION : 0.1
   STRUCT
      Run : Bool;   // running
      Fault : Bool;
      Speed : Int;
      Current : Real;
   END_STRUCT;

END_TYPE

DATA_BLOCK "Line_Data"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
NON_RETAIN
   STRUCT
      Start : Bool;   // start button
      Stop { ExternalAccessible := 'False'} : Bool;
      Mode : Byte;
      Count : DInt := 5;
      Name : String[10] := 'abc';
      Bits : Array[0..9] of Bool;
      Temps : Array[1..3] of Real;
      M1 : "Motor_T";
      Grid : Array[0..1, 0..1] of Int;
      Flag : Bool;
      St : Struct
         a : Byte;
         b : Time := T#5s;
      END_STRUCT;
   END_STRUCT;


BEGIN
   Count := 7;

END_DATA_BLOCK

DATA_BLOCK "Opt"
{ S7_Optimized_Access := 'TRUE' }
VERSION : 0.1
   STRUCT
      x : Int;
   END_STRUCT;
BEGIN
END_DATA_BLOCK

DATA_BLOCK "IDB_Pump"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
NON_RETAIN
"FB_Pump"

BEGIN
END_DATA_BLOCK

DATA_BLOCK "MotorDB"
VERSION : 0.1
"Motor_T"
BEGIN
END_DATA_BLOCK
`

const awlSource = `DATA_BLOCK DB 5
TITLE =Classic block
VERSION : 0.1

  STRUCT
   Level : REAL ;	//tank level
   Alarms : ARRAY  [1 .. 20 ] OF BOOL ;
   Count : INT ;
  END_STRUCT ;
BEGIN
   Level := 0.000000e+000;
END_DATA_BLOCK
`

func symMap(b SymBlock) map[string]Symbol {
	m := map[string]Symbol{}
	for _, s := range b.Symbols {
		m[s.Name] = s
	}
	return m
}

func findBlock(t *testing.T, bs []SymBlock, name string) SymBlock {
	t.Helper()
	for _, b := range bs {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("block %s missing", name)
	return SymBlock{}
}

func TestSourceLayout(t *testing.T) {
	blocks, notes, err := parseFiles([]symFile{{"line.db", []byte(tiaSource)}, {"old.awl", []byte(awlSource)}}, map[string]int{"Line_Data": 10})
	if err != nil {
		t.Fatal(err, notes)
	}
	b := findBlock(t, blocks, "Line_Data")
	if b.DB != 10 || b.Size != 56 || b.Optimized || b.Note != "" {
		t.Errorf("Line_Data: db %d size %d note %q", b.DB, b.Size, b.Note)
	}
	m := symMap(b)
	want := []struct {
		name     string
		off, bit int
		typ      string
	}{
		{"Start", 0, 0, "BOOL"}, {"Stop", 0, 1, "BOOL"}, {"Mode", 1, 0, "BYTE"}, {"Count", 2, 0, "DINT"},
		{"Name", 6, 0, ""}, {"Bits[0]", 18, 0, "BOOL"}, {"Bits[9]", 19, 1, "BOOL"},
		{"Temps[1]", 20, 0, "REAL"}, {"Temps[3]", 28, 0, "REAL"},
		{"M1.Run", 32, 0, "BOOL"}, {"M1.Fault", 32, 1, "BOOL"}, {"M1.Speed", 34, 0, "INT"}, {"M1.Current", 36, 0, "REAL"},
		{"Grid[0,0]", 40, 0, "INT"}, {"Grid[0,1]", 42, 0, "INT"}, {"Grid[1,1]", 46, 0, "INT"},
		{"Flag", 48, 0, "BOOL"}, {"St.a", 50, 0, "BYTE"}, {"St.b", 52, 0, "DINT"},
	}
	for _, w := range want {
		s, ok := m[w.name]
		if !ok {
			t.Errorf("%s missing", w.name)
			continue
		}
		if s.Offset != w.off || s.Bit != w.bit || s.Type != w.typ {
			t.Errorf("%s at %d.%d %s, want %d.%d %s", w.name, s.Offset, s.Bit, s.Type, w.off, w.bit, w.typ)
		}
	}
	if m["Start"].Comment != "start button" || m["M1.Run"].Comment != "running" {
		t.Errorf("comments: %q %q", m["Start"].Comment, m["M1.Run"].Comment)
	}
	if !findBlock(t, blocks, "Opt").Optimized {
		t.Error("optimized block not flagged")
	}
	if n := findBlock(t, blocks, "IDB_Pump").Note; !strings.Contains(n, "FB_Pump") || !strings.Contains(n, "not imported") {
		t.Errorf("instance DB note: %q", n)
	}
	if md := symMap(findBlock(t, blocks, "MotorDB")); md["Current"].Offset != 4 || md["Fault"].Bit != 1 {
		t.Errorf("DB from a type: %+v", md)
	}
	c := findBlock(t, blocks, "DB 5")
	cm := symMap(c)
	if c.DB != 5 || cm["Level"].Type != "REAL" || cm["Level"].Comment != "tank level" || cm["Alarms[20]"].Offset != 6 || cm["Alarms[20]"].Bit != 3 || cm["Count"].Offset != 8 {
		t.Errorf("classic source: db %d %+v", c.DB, cm)
	}
}

func TestSourceErrors(t *testing.T) {
	if _, _, err := parseFiles([]symFile{{"x.db", []byte("hello world")}}, nil); err == nil {
		t.Error("a file without blocks was accepted")
	}
	blocks, _, err := parseFiles([]symFile{{"x.db", []byte(`DATA_BLOCK "A" STRUCT u : "Missing"; END_STRUCT; BEGIN END_DATA_BLOCK`)}}, nil)
	if err != nil || !strings.Contains(blocks[0].Note, "unknown") {
		t.Errorf("unknown type: %v %+v", err, blocks)
	}
	if _, _, err := parseFiles([]symFile{{"x.db", []byte(`DATA_BLOCK "A" STRUCT u : Int; `)}}, nil); err == nil {
		t.Error("missing END_STRUCT accepted")
	}
}

// makeXLSX builds a minimal .xlsx with shared strings, as TIA exports it.
func makeXLSX(t *testing.T, rows [][]string) []byte {
	var shared []string
	idx := map[string]int{}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for i, r := range rows {
		sheet.WriteString(`<row>`)
		for j, c := range r {
			if c == "" {
				continue
			}
			n, ok := idx[c]
			if !ok {
				n = len(shared)
				idx[c] = n
				shared = append(shared, c)
			}
			sheet.WriteString(`<c r="` + string(rune('A'+j)) + strconv.Itoa(i+1) + `" t="s"><v>` + strconv.Itoa(n) + `</v></c>`)
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	var sst strings.Builder
	sst.WriteString(`<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, s := range shared {
		sst.WriteString(`<si><t>` + s + `</t></si>`)
	}
	sst.WriteString(`</sst>`)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"xl/worksheets/sheet1.xml": sheet.String(), "xl/sharedStrings.xml": sst.String()} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTagTableImport(t *testing.T) {
	x := makeXLSX(t, [][]string{
		{"Name", "Path", "Data Type", "Logical Address", "Comment"},
		{"Start_PB", "Default tag table", "Bool", "%I0.0", "start"},
		{"Speed_SP", "Default tag table", "Int", "%MW20", ""},
		{"Valve", "Default tag table", "Bool", "%Q4.7", ""},
		{"Total", "Default tag table", "Real", "%MD100", ""},
		{"Tmr", "Default tag table", "Timer", "%T1", ""},
	})
	blocks, notes, err := parseFiles([]symFile{{"PLC tags.xlsx", x}}, nil)
	if err != nil {
		t.Fatal(err, notes)
	}
	m := symMap(blocks[0])
	if len(m) != 4 || m["Start_PB"].Area != "I" || m["Start_PB"].Comment != "start" || m["Speed_SP"].Type != "INT" || m["Speed_SP"].Offset != 20 ||
		m["Valve"].Area != "Q" || m["Valve"].Bit != 7 || m["Total"].Type != "REAL" || blocks[0].Kind != "tags" {
		t.Errorf("xlsx: %+v", m)
	}
	csvData := "Name;Data Type;Logical Address;Comment\nLamp;Bool;%Q0.1;red lamp\n"
	blocks, _, err = parseFiles([]symFile{{"tags.csv", []byte(csvData)}}, nil)
	if err != nil || len(blocks[0].Symbols) != 1 || blocks[0].Symbols[0].Comment != "red lamp" {
		t.Errorf("csv: %v %+v", err, blocks)
	}
	if _, _, err := parseFiles([]symFile{{"bad.xlsx", []byte("nope")}}, nil); err == nil {
		t.Error("bad xlsx accepted")
	}
}

// The tag table's Export from TIA Portal (as written by TIA V17).
const tiaTagtableXML = `<?xml version='1.0' encoding='utf-8'?>
<Tagtable name='ControllerTagsFolder'>
  <!-- exported by 'Totally Integrated Automation Portal' version 1700.4.401.1 -->
  <Tag type='Bool' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='' addr='%Q0.4'>ValvaClean</Tag>
  <Tag type='Bool' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='e-stop chain' addr='%I0.0'>EStop_OK</Tag>
  <Tag type='Bool' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='' addr='%I0.4'>Fluff01Mi n</Tag>
  <Tag type='Byte' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='' addr='%MB0'>Clock_Byte</Tag>
  <Tag type='Bool' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='' addr='%M0.2'>Clock_2.5Hz</Tag>
  <Tag type='Int' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='' addr='%MW21'>Silo_Level_AI</Tag>
  <Tag type='Int' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='' addr='%QW64'>AnalogToBlower</Tag>
  <Tag type='String' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark='' addr='%MB100'>Text</Tag>
  <Tag type='Bool' hmiVisible='True' hmiWriteable='True' hmiAccessible='True' retain='False' remark=''>NoAddress</Tag>
</Tagtable>`

// TIA Openness export of a tag table.
const tiaOpennessXML = "\xEF\xBB\xBF" + `<?xml version="1.0" encoding="utf-8"?>
<Document>
  <SW.Tags.PlcTagTable ID="0">
    <AttributeList><Name>Default tag table</Name></AttributeList>
    <ObjectList>
      <SW.Tags.PlcTag ID="1" CompositionName="Tags">
        <AttributeList>
          <DataTypeName>Real</DataTypeName>
          <ExternalAccessible>true</ExternalAccessible>
          <LogicalAddress>%MD8</LogicalAddress>
          <Name>Flow</Name>
        </AttributeList>
        <ObjectList>
          <MultilingualText ID="2" CompositionName="Comment">
            <ObjectList>
              <MultilingualTextItem ID="3" CompositionName="Items">
                <AttributeList><Culture>en-US</Culture><Text>flow m3/h</Text></AttributeList>
              </MultilingualTextItem>
            </ObjectList>
          </MultilingualText>
        </ObjectList>
      </SW.Tags.PlcTag>
      <SW.Tags.PlcTag ID="4" CompositionName="Tags">
        <AttributeList>
          <DataTypeName>Bool</DataTypeName>
          <LogicalAddress>%Q2.3</LogicalAddress>
          <Name>Pump</Name>
        </AttributeList>
      </SW.Tags.PlcTag>
    </ObjectList>
  </SW.Tags.PlcTagTable>
</Document>`

func TestTagTableXML(t *testing.T) {
	tags, skipped, err := TIATags("PLCTags.xml", []byte(tiaTagtableXML))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Tag{}
	for _, x := range tags {
		m[x.Name] = x
	}
	want := map[string]Tag{
		"ValvaClean":     {Area: "Q", Offset: 0, Bit: 4, Type: "BOOL"},
		"EStop_OK":       {Area: "I", Offset: 0, Bit: 0, Type: "BOOL", Description: "e-stop chain"},
		"Fluff01Mi n":    {Area: "I", Offset: 0, Bit: 4, Type: "BOOL"},
		"Clock_Byte":     {Area: "M", Offset: 0, Type: "BYTE"},
		"Clock_2.5Hz":    {Area: "M", Offset: 0, Bit: 2, Type: "BOOL"},
		"Silo_Level_AI":  {Area: "M", Offset: 21, Type: "INT"},
		"AnalogToBlower": {Area: "Q", Offset: 64, Type: "INT"},
	}
	if len(tags) != len(want) || skipped != 2 {
		t.Errorf("%d tags, %d skipped: %+v", len(tags), skipped, tags)
	}
	for n, w := range want {
		g := m[n]
		w.Name = n
		if g != w {
			t.Errorf("%s: %+v, want %+v", n, g, w)
		}
	}
	// The tags pass the config's checks.
	cfg := &Config{Tags: tags}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Errorf("validate: %v", err)
	}

	tags, _, err = TIATags("tags.xml", []byte(tiaOpennessXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0] != (Tag{Name: "Flow", Area: "M", Offset: 8, Type: "REAL", Description: "flow m3/h"}) ||
		tags[1] != (Tag{Name: "Pump", Area: "Q", Offset: 2, Bit: 3, Type: "BOOL"}) {
		t.Errorf("openness: %+v", tags)
	}

	// Scan PLC takes the XML as names too.
	blocks, _, err := parseFiles([]symFile{{"PLCTags.xml", []byte(tiaTagtableXML)}}, nil)
	if err != nil || len(blocks) != 1 || blocks[0].Kind != "tags" || symMap(blocks[0])["Silo_Level_AI"].Offset != 21 {
		t.Errorf("parseFiles: %v %+v", err, blocks)
	}

	for name, data := range map[string]string{
		"x.xml": "<Document><Other/></Document>", "y.xml": "not xml <", "z.db": "DATA_BLOCK x",
		"w.xml": `<?xml version="1.0" encoding="windows-1252"?><Tagtable/>`,
	} {
		if _, _, err := TIATags(name, []byte(data)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// An FB instance DB with standard access, as a 1200-series CPU lays it out
// (offsets read back from a real PLC): sections start on an even byte, an
// R_TRIG's bits go into the section's last Bool byte, IEC timers are 16
// bytes with PT at +4.
const fbSource = `TYPE "typeStat"
VERSION : 0.1
   STRUCT
      A : Bool;
      B : Bool;
   END_STRUCT;
END_TYPE

FUNCTION_BLOCK "FB_Motor"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
   VAR_INPUT
      AutoStart : Bool;
      AutoStop : Bool;
      Feedback : Bool;
      FbTimeout : Time := T#3S;
      SafeInterlock : Bool;
   END_VAR

   VAR_OUTPUT
      Run : Bool := false;
      Status : "typeStat";
      CtrlLevel : Int;
   END_VAR

   VAR_IN_OUT
      Ref : Array[0..3] of Int;
      Count : Int;
   END_VAR

   VAR
      ManStart : Bool;
      tmNoFb {InstructionName := 'TON_TIME'; LibVersion := '1.0'; S7_SetPoint := 'False'} : TON_TIME;
      rtFb {InstructionName := 'R_TRIG'; LibVersion := '1.0'} : R_TRIG;
   END_VAR

   VAR_TEMP
      doRun : Bool;
   END_VAR

   VAR CONSTANT
      MAX : Int := 10;
   END_VAR


BEGIN
	#Run := #AutoStart; // VAR_INPUT in a comment does not matter
	IF #Run THEN #CtrlLevel := 1; END_IF;
END_FUNCTION_BLOCK

FUNCTION_BLOCK "FB_Top"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
   VAR
      Flag : Bool;
      rtReset {InstructionName := 'R_TRIG'; LibVersion := '1.0'} : R_TRIG;
      M1 : "FB_Motor";
      M2 : "FB_Motor";
   END_VAR

BEGIN
END_FUNCTION_BLOCK

DATA_BLOCK "FB_Top_DB"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
NON_RETAIN
"FB_Top"

BEGIN

END_DATA_BLOCK

DATA_BLOCK "IEC_Timer_0_DB"
{ S7_Optimized_Access := 'FALSE' }
VERSION : 0.1
NON_RETAIN
TON_TIME

BEGIN

END_DATA_BLOCK

DATA_BLOCK "Missing_DB"
VERSION : 0.1
"FB_Unknown"
BEGIN
END_DATA_BLOCK

FUNCTION_BLOCK "FB_Opt"
{ S7_Optimized_Access := 'TRUE' }
VERSION : 0.1
   VAR_INPUT
      X : Bool;
   END_VAR
BEGIN
END_FUNCTION_BLOCK

DATA_BLOCK "Opt_DB"
"FB_Opt"
BEGIN
END_DATA_BLOCK
`

func TestFBInstanceLayout(t *testing.T) {
	blocks, notes, err := parseFiles([]symFile{{"fb.scl", []byte(fbSource)}}, nil)
	if err != nil {
		t.Fatal(err, notes)
	}
	top := findBlock(t, blocks, "FB_Top_DB")
	if top.Note != "" {
		t.Fatal(top.Note)
	}
	m := symMap(top)
	at := func(name string, off, bit int) {
		t.Helper()
		s, ok := m[name]
		if !ok {
			t.Errorf("%s missing", name)
			return
		}
		if s.Offset != off || s.Bit != bit {
			t.Errorf("%s at %d.%d, want %d.%d", name, s.Offset, s.Bit, off, bit)
		}
	}
	// FB_Top: Flag 0.0, rtReset packs into 0.1-0.3, M1 at 2.
	at("Flag", 0, 0)
	at("rtReset.CLK", 0, 1)
	at("rtReset.Stat_Bit", 0, 3)
	// FB_Motor at 2: inputs 2.0-2.2, FbTimeout 4, SafeInterlock 8.0;
	// outputs from 10: Run 10.0, Status 12, CtrlLevel 14;
	// in/out from 16: Ref is a 6-byte pointer 16, Count 22;
	// statics from 24: ManStart 24.0, rtFb into 24.1-24.3, tmNoFb 26 (PT 30, ET 34, IN 38.1, Q 38.2).
	at("M1.AutoStart", 2, 0)
	at("M1.FbTimeout", 4, 0)
	at("M1.SafeInterlock", 8, 0)
	at("M1.Run", 10, 0)
	at("M1.Status.A", 12, 0)
	at("M1.CtrlLevel", 14, 0)
	at("M1.Ref", 16, 0)
	at("M1.Count", 22, 0)
	at("M1.ManStart", 24, 0)
	at("M1.rtFb.CLK", 24, 1)
	at("M1.rtFb.Stat_Bit", 24, 3)
	at("M1.tmNoFb.PT", 30, 0)
	at("M1.tmNoFb.ET", 34, 0)
	at("M1.tmNoFb.IN", 38, 1)
	at("M1.tmNoFb.Q", 38, 2)
	at("M2.AutoStart", 42, 0) // FB_Motor is 40 bytes
	if top.Size != 82 {
		t.Errorf("FB_Top_DB size %d, want 82", top.Size)
	}
	for _, bad := range []string{"M1.doRun", "M1.MAX"} {
		if _, ok := m[bad]; ok {
			t.Errorf("%s (temp / constant) laid out", bad)
		}
	}
	if s := m["M1.tmNoFb.PT"]; s.Type != "DINT" || s.Size != 4 {
		t.Errorf("timer PT: %+v", s)
	}

	timer := findBlock(t, blocks, "IEC_Timer_0_DB")
	if tm := symMap(timer); timer.Size != 16 || tm["PT"].Offset != 4 || tm["Q"].Bit != 2 {
		t.Errorf("timer instance DB: size %d %+v", timer.Size, tm)
	}
	if n := findBlock(t, blocks, "Missing_DB").Note; !strings.Contains(n, "FB_Unknown") {
		t.Errorf("missing FB note: %q", n)
	}
	if !findBlock(t, blocks, "Opt_DB").Optimized {
		t.Error("instance DB of an optimized FB not flagged")
	}
}

func TestSymbolStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plc-symbols.json")
	s := NewSymbolStore(path)
	if _, err := s.Import("bad.db", []byte("nothing here"), nil); err == nil {
		t.Error("bad file imported")
	}
	// Line_Data is 56 bytes: the scan found exactly one DB of that size.
	found := []PLCArea{{"DB", 3, 56}, {"DB", 4, 10}}
	st, err := s.Import("line.db", []byte(tiaSource), found)
	if err != nil {
		t.Fatal(err)
	}
	if b := findBlock(t, st.Blocks, "Line_Data"); b.DB != 3 {
		t.Errorf("DB number not guessed from the size: %d", b.DB)
	}
	if err := s.SetDB("Opt", 3); err == nil {
		t.Error("the same DB number given twice")
	}
	if err := s.SetDB("Opt", 12); err != nil {
		t.Fatal(err)
	}
	// Saved and loaded again.
	s2 := NewSymbolStore(path)
	st2 := s2.State()
	if len(st2.Files) != 1 || findBlock(t, st2.Blocks, "Opt").DB != 12 || findBlock(t, st2.Blocks, "Line_Data").DB != 3 {
		t.Errorf("after reload: %+v", st2.Files)
	}
	if err := s2.Remove("line.db"); err != nil || len(s2.State().Blocks) != 0 {
		t.Errorf("remove: %v", err)
	}
}
