//go:build gui && windows

package main

// The desktop window comes back as it was left: size, position, maximized,
// and the zoom of its pages (View → Zoom, the − / + in the menu bar, or
// Ctrl+mouse wheel). Kept per user in %LOCALAPPDATA%\PLCMonitor\window.json,
// not in the configuration: it belongs to this PC's screen.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
)

type windowState struct {
	X, Y, W, H int
	Max        bool
	Zoom       float64
}

var (
	procGetWindowPlacement = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement = user32.NewProc("SetWindowPlacement")
	procMonitorFromRect    = user32.NewProc("MonitorFromRect")
	appZoom                = 1.0
)

type windowPlacement struct {
	length, flags, showCmd uint32
	minX, minY, maxX, maxY int32
	normal                 rect
}

func windowStatePath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "PLCMonitor", "window.json")
}

// restoreWindowState puts the main window where it was and sets the zoom.
func restoreWindowState(hwnd uintptr) {
	b, err := os.ReadFile(windowStatePath())
	if err != nil {
		return
	}
	var s windowState
	if json.Unmarshal(b, &s) != nil {
		return
	}
	if s.Zoom >= 0.25 && s.Zoom <= 5 {
		appZoom = s.Zoom
	}
	if s.W < 300 || s.H < 200 {
		return
	}
	r := rect{int32(s.X), int32(s.Y), int32(s.X + s.W), int32(s.Y + s.H)}
	// Only on a screen that is there now (a second monitor may be gone).
	if m, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), 0 /*MONITOR_DEFAULTTONULL*/); m == 0 {
		return
	}
	wp := windowPlacement{showCmd: 1 /*SW_SHOWNORMAL*/, normal: r}
	if s.Max {
		wp.showCmd = 3 // SW_SHOWMAXIMIZED
	}
	wp.length = uint32(unsafe.Sizeof(wp))
	procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
}

// saveWindowState writes the window's place and the zoom (when it closes).
func saveWindowState(hwnd uintptr) {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); r == 0 {
		return
	}
	n := wp.normal
	s := windowState{X: int(n.left), Y: int(n.top), W: int(n.right - n.left), H: int(n.bottom - n.top), Max: wp.showCmd == 3, Zoom: appZoom}
	if z, ok := mainZoom(); ok {
		s.Zoom = z // also a zoom set with Ctrl+mouse wheel
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	os.MkdirAll(filepath.Dir(windowStatePath()), 0o755)
	os.WriteFile(windowStatePath(), b, 0o644)
}

// ---- zoom: ICoreWebView2Controller get_ZoomFactor (7) / put_ZoomFactor (8) ----

func controllerVtbl(c *edge.ICoreWebView2Controller) []uintptr {
	if c == nil {
		return nil
	}
	vt := *(**[9]uintptr)(unsafe.Pointer(c)) // a COM object starts with its function table
	return vt[:]
}

func getZoom(c *edge.ICoreWebView2Controller) (float64, bool) {
	vt := controllerVtbl(c)
	if vt == nil {
		return 0, false
	}
	var z float64
	r, _, _ := syscall.SyscallN(vt[7], uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(&z)))
	return z, r == 0 && z > 0
}

func putZoom(c *edge.ICoreWebView2Controller, z float64) {
	vt := controllerVtbl(c)
	if vt == nil {
		return
	}
	bits := math.Float64bits(z)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		// x64: a double goes in XMM1; Go's stdcall copies the integer
		// arguments into the XMM registers too.
		syscall.SyscallN(vt[8], uintptr(unsafe.Pointer(c)), uintptr(bits))
	} else { // x86: a double is two words on the stack
		syscall.SyscallN(vt[8], uintptr(unsafe.Pointer(c)), uintptr(uint32(bits)), uintptr(uint32(bits>>32)))
	}
}

// mainChromium is the main window's WebView2 (go-webview2 keeps it private).
func mainChromium(w webview2.WebView) *edge.Chromium {
	v := reflect.ValueOf(w)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	f := v.Elem().FieldByName("browser")
	if !f.IsValid() || !f.CanAddr() {
		return nil
	}
	f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	ch, _ := f.Interface().(*edge.Chromium)
	return ch
}

func mainZoom() (float64, bool) {
	if mainView == nil {
		return 0, false
	}
	ch := mainChromium(mainView)
	if ch == nil {
		return 0, false
	}
	return getZoom(ch.GetController())
}

// setZoom applies a zoom to every window and tells the pages (the % shown).
func setZoom(z float64) {
	z = math.Round(math.Min(3, math.Max(0.5, z))*100) / 100
	appZoom = z
	if ch := mainChromium(mainView); ch != nil {
		putZoom(ch.GetController(), z)
	}
	for _, c := range children {
		if c.chromium != nil {
			putZoom(c.chromium.GetController(), z)
		}
	}
	tellZoom()
}

func tellZoom() {
	js := "window.__s7zoomShown && window.__s7zoomShown(" + strconv.FormatFloat(appZoom, 'f', 2, 64) + ")"
	if mainView != nil {
		mainView.Eval(js)
	}
}

// zoomStep: "+" / "-" one step, "0" back to 100 %, "?" just report.
func zoomStep(op string) {
	if z, ok := mainZoom(); ok {
		appZoom = z // Ctrl+mouse wheel may have changed it
	}
	steps := []float64{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}
	switch op {
	case "+":
		for _, s := range steps {
			if s > appZoom+0.005 {
				setZoom(s)
				return
			}
		}
	case "-":
		for i := len(steps) - 1; i >= 0; i-- {
			if steps[i] < appZoom-0.005 {
				setZoom(steps[i])
				return
			}
		}
	case "0":
		setZoom(1)
		return
	}
	tellZoom()
}
