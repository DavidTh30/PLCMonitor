package main

// The UDT tool window: build a PLC data type (UDT) from tags in use, or
// import one (a UDT sheet, or a TYPE / DATA_BLOCK from an SCL source) and
// edit it; export it as an SCL source (TYPE … END_TYPE) or as a UDT sheet.

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var udtTypeNameRe = regexp.MustCompile(`(?im)^\s*TYPE\s+(?:"([^"]+)"|([A-Za-z_]\w*))`)

// sclTypeText writes a parsed type as SCL.
func sclTypeText(t *symType) string {
	switch t.kind {
	case kString:
		w := "String"
		if t.wide {
			w = "WString"
		}
		if t.strLen > 0 {
			return fmt.Sprintf("%s[%d]", w, t.strLen)
		}
		return w
	case kArray:
		d := make([]string, len(t.dims))
		for i, x := range t.dims {
			d[i] = fmt.Sprintf("%d..%d", x[0], x[1])
		}
		return "Array[" + strings.Join(d, ", ") + "] of " + sclTypeText(t.elem)
	case kRef, kFB:
		return `"` + t.name + `"`
	case kStruct:
		return "Struct"
	}
	if t.orig != "" {
		return t.orig
	}
	if n := sclTypeNames[t.name]; n != "" {
		return n
	}
	return t.name
}

// udtMembersOf flattens a structure into dotted members. A member of another
// UDT is written out as a Struct with that UDT's members (the tool edits one
// type), with the UDT's name in the comment.
func udtMembersOf(members []symMember, types map[string]*symType, prefix string, depth int, out *[]UDTMember) {
	for _, m := range members {
		name := m.name
		if prefix != "" {
			name = prefix + "." + m.name
		}
		t := m.typ
		if t.kind == kRef {
			if ref := types[t.name]; ref != nil && ref.kind == kStruct && depth < 8 {
				c := m.comment
				if c == "" {
					c = `"` + t.name + `"`
				}
				*out = append(*out, UDTMember{Name: name, Type: "Struct", Comment: c})
				udtMembersOf(ref.members, types, name, depth+1, out)
				continue
			}
		}
		if t.kind == kStruct {
			*out = append(*out, UDTMember{Name: name, Type: "Struct", Comment: m.comment})
			udtMembersOf(t.members, types, name, depth+1, out)
			continue
		}
		*out = append(*out, UDTMember{Name: name, Type: sclTypeText(t), Comment: m.comment})
	}
}

// UDTCandidate is a type found in an imported file.
type UDTCandidate struct {
	Name    string      `json:"name"`
	Kind    string      `json:"kind"` // udt, db or sheet
	Members []UDTMember `json:"members"`
}

// ImportUDT reads the UDTs of a file: a UDT sheet (.xlsx, .xls, .csv), or the
// TYPEs and DATA_BLOCKs of a source (.udt, .scl, .db).
func ImportUDT(name string, data []byte) ([]UDTCandidate, error) {
	base := udtName(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
	if isBlockSource(name) {
		types, dbs, err := parseSource(string(data))
		if err != nil {
			return nil, err
		}
		// The parser keys types in upper case: the names as written.
		orig := map[string]string{}
		for _, m := range udtTypeNameRe.FindAllStringSubmatch(string(data), -1) {
			n := m[1] + m[2]
			orig[strings.ToUpper(n)] = n
		}
		var out []UDTCandidate
		names := make([]string, 0, len(types))
		for n := range types {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			t := types[n]
			if t.kind != kStruct {
				continue
			}
			c := UDTCandidate{Name: n, Kind: "udt"}
			if o := orig[n]; o != "" {
				c.Name = o
			}
			udtMembersOf(t.members, types, "", 0, &c.Members)
			out = append(out, c)
		}
		for _, d := range dbs {
			if d.typ == nil || d.typ.kind != kStruct {
				continue // an instance DB, or a DB of a UDT (the UDT is listed)
			}
			c := UDTCandidate{Name: d.name, Kind: "db"}
			udtMembersOf(d.typ.members, types, "", 0, &c.Members)
			out = append(out, c)
		}
		if len(out) == 0 {
			return nil, errors.New("no TYPE (UDT) or DATA_BLOCK with variables in the source")
		}
		return out, nil
	}
	rows, err := sheetRows(name, data)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, errors.New("open a UDT sheet (.xlsx, .xls, .csv) or a source (.udt, .scl, .db)")
	}
	ms, _, ok := udtSheetMembers(rows)
	if !ok {
		return nil, errors.New("not a UDT: the sheet needs the columns Name and Data Type (and no Address column – that is a tag table)")
	}
	if len(ms) == 0 {
		return nil, errors.New("the UDT sheet has no members")
	}
	for i := range ms {
		ms[i].Offset = "" // laid out again
	}
	return []UDTCandidate{{Name: base, Kind: "sheet", Members: ms}}, nil
}

// ExportUDT writes a UDT as an SCL source (scl) or a UDT sheet (xlsx, csv).
func ExportUDT(name string, ms []UDTMember, format string) (file string, data []byte, err error) {
	lay, err := LayoutUDT(name, ms)
	if err != nil {
		return "", nil, err
	}
	switch format {
	case "scl", "udt":
		src := "// Written by PLC Monitor " + version + " – " + strconv.Itoa(lay.Size) + " bytes, standard access\n" + lay.SCL
		return lay.Name + "." + format, []byte(strings.ReplaceAll(src, "\n", "\r\n")), nil
	case "csv":
		var b bytes.Buffer
		b.WriteString("\xEF\xBB\xBF")
		w := csv.NewWriter(&b)
		w.UseCRLF = true
		w.Write([]string{"Name", "Data Type", "Offset", "Comment"})
		for _, m := range lay.Members {
			w.Write([]string{m.Name, m.Type, m.Offset, m.Comment})
		}
		w.Flush()
		return lay.Name + ".csv", b.Bytes(), w.Error()
	case "xlsx", "":
		rows := make([][]any, len(lay.Members))
		for i, m := range lay.Members {
			rows[i] = []any{m.Name, m.Type, m.Offset, m.Comment}
		}
		data, err := xlsxWorkbook("UDT", []string{"Name", "Data Type", "Offset", "Comment"}, rows, []float64{30, 22, 9, 40})
		return lay.Name + ".xlsx", data, err
	}
	return "", nil, fmt.Errorf("format must be scl, udt, xlsx or csv")
}

// udtRoutes: the UDT tool's API.
//
//	POST /api/udt/layout {name, members}         → the laid-out UDT
//	POST /api/udt/import {name, data}            → {types: [{name, kind, members}]}
//	POST /api/udt/export {name, members, format} → {name, data}
func udtRoutes(mux *http.ServeMux) {
	type req struct {
		Name    string      `json:"name"`
		Members []UDTMember `json:"members"`
		Format  string      `json:"format"`
		Data    []byte      `json:"data"`
	}
	read := func(w http.ResponseWriter, r *http.Request) (req, bool) {
		var q req
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&q); err != nil {
			respond(w, fmt.Errorf("bad JSON: %w", err))
			return q, false
		}
		return q, true
	}
	mux.HandleFunc("POST /api/udt/layout", func(w http.ResponseWriter, r *http.Request) {
		q, ok := read(w, r)
		if !ok {
			return
		}
		lay, err := LayoutUDT(q.Name, q.Members)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, lay)
	})
	mux.HandleFunc("POST /api/udt/import", func(w http.ResponseWriter, r *http.Request) {
		q, ok := read(w, r)
		if !ok {
			return
		}
		types, err := ImportUDT(q.Name, q.Data)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"types": types})
	})
	mux.HandleFunc("POST /api/udt/export", func(w http.ResponseWriter, r *http.Request) {
		q, ok := read(w, r)
		if !ok {
			return
		}
		file, data, err := ExportUDT(q.Name, q.Members, q.Format)
		if err != nil {
			respond(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": file, "data": data})
	})
}
