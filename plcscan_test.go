package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPLCScan finds the simulator's data blocks and M area, measures them
// and reads their bytes through the API.
func TestPLCScan(t *testing.T) {
	mon, sim := newTestMonitor(t, []Tag{
		{Name: "a", Area: "DB", DB: 1, Offset: 0, Type: "REAL"},
		{Name: "b", Area: "DB", DB: 7, Offset: 10, Type: "INT"},
		{Name: "c", Area: "M", Offset: 2, Type: "BYTE"},
	})
	go sim.Run(t.Context())
	go mon.Run(t.Context())
	waitFor(t, "connected", func() bool { st, _ := mon.Snapshot(); return st.Connected })
	h := NewWebHandler(mon, false)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("POST", "/api/plcscan", `{"from":5,"to":1}`); rec.Code == 200 {
		t.Error("from > to accepted")
	}
	if rec := call("POST", "/api/plcscan", `{"from":1,"to":100,"areas":true,"block_list":true}`); rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	var st PLCScan
	deadline := time.Now().Add(10 * time.Second)
	for {
		json.Unmarshal(call("GET", "/api/plcscan", "").Body.Bytes(), &st)
		if !st.Running || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Running || st.Error != "" {
		t.Fatalf("scan: running %v error %q", st.Running, st.Error)
	}
	// The simulator's areas are as large as the tags need plus 64 bytes.
	want := []PLCArea{{"DB", 1, 68}, {"DB", 7, 76}, {"M", 0, 67}}
	if len(st.Found) != len(want) {
		t.Fatalf("found %+v", st.Found)
	}
	for i := range want {
		if st.Found[i] != want[i] {
			t.Errorf("found[%d] = %+v, want %+v", i, st.Found[i], want[i])
		}
	}
	if st.Note == "" || st.Done != 100 {
		t.Errorf("no note about the missing block list, or done %d: %+v", st.Done, st)
	}

	// Write a known value, then read it back through the byte view.
	if err := mon.Write("b", int64(-2)); err != nil {
		t.Fatal(err)
	}
	rec := call("GET", "/api/plcscan/read?area=DB&db=7&start=8&size=4", "")
	var got struct{ Data []byte }
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || len(got.Data) != 4 || got.Data[2] != 0xFF || got.Data[3] != 0xFE {
		t.Errorf("read: %d %s", rec.Code, rec.Body)
	}
	if rec := call("GET", "/api/plcscan/read?area=DB&db=7&start=60&size=40", ""); rec.Code == 200 {
		t.Error("read past the end of the DB succeeded")
	}
	if rec := call("GET", "/api/plcscan/read?area=HR&db=1&start=0&size=4", ""); rec.Code == 200 {
		t.Error("Modbus area accepted")
	}
	if rec := NewWebHandler(mon, true); rec != nil {
		req := httptest.NewRequest("POST", "/api/plcscan", strings.NewReader(`{"from":1,"to":2}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		rec.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Errorf("scan on a read-only dashboard: %d", w.Code)
		}
	}
}
