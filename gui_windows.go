//go:build gui && windows

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

const guiBuild = true

var (
	user32                            = windows.NewLazySystemDLL("user32.dll")
	procGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procCreateIconFromResourceEx      = user32.NewProc("CreateIconFromResourceEx")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procSendMessageW                  = user32.NewProc("SendMessageW")
)

func init() {
	// The window and its message loop must stay on the main OS thread.
	runtime.LockOSThread()
	// Render sharply on scaled displays instead of being bitmap-stretched.
	// -4 = DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (Windows 10 1703+).
	if procSetProcessDpiAwarenessContext.Find() == nil {
		procSetProcessDpiAwarenessContext.Call(^uintptr(3))
	}
}

// runWindow shows the dashboard in a native window (Microsoft Edge WebView2,
// built into Windows 10/11) and blocks until the user closes it. title is
// polled to keep the window and taskbar caption showing the link state.
func runWindow(url string, title func() string) error {
	scale := func(px uint) uint { return px * uint(systemDPI()) / 96 }
	// The window's data of the earlier name (S7Monitor: theme, layout,
	// recent files) moves to PLCMonitor the first time.
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		if _, err := os.Stat(filepath.Join(la, "PLCMonitor")); os.IsNotExist(err) {
			os.Rename(filepath.Join(la, "S7Monitor"), filepath.Join(la, "PLCMonitor"))
		}
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		// Keep the browser profile out of the program folder, which may be
		// read-only (e.g. under Program Files).
		DataPath:  filepath.Join(os.Getenv("LOCALAPPDATA"), "PLCMonitor", "WebView2"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "PLC Monitor",
			Width:  scale(1280),
			Height: scale(820),
			Center: true,
		},
	})
	if w == nil {
		return errors.New("could not open the PLC Monitor window.\n\n" +
			"The Microsoft Edge WebView2 Runtime may be missing. Install it from\n" +
			"https://developer.microsoft.com/microsoft-edge/webview2/")
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	setWindowIcon(hwnd)
	setupWindows(w)   // other windows open as windows of this program (gui_children.go)
	customFrame(hwnd) // the page draws the title bar

	stop := make(chan struct{})
	defer close(stop)

	// File → Exit closes the window; main then shuts down cleanly.
	exitApp = func() {
		log.Printf("exit requested from the File menu")
		w.Dispatch(w.Terminate)
	}

	// File menu: show the common Open/Save dialogs on the UI thread, owned
	// by our window so they are modal to it.
	nativeFileDialog = func(k FileKind, save bool, current string) (string, bool) {
		res := make(chan [2]any, 1)
		w.Dispatch(func() {
			p, ok := fileDialog(hwnd, k, save, current)
			res <- [2]any{p, ok}
		})
		select {
		case r := <-res:
			return r[0].(string), r[1].(bool)
		case <-stop:
			return "", false
		}
	}
	// View → Always on top. Applies to the main window at once, and to the
	// Network/Topology/Export/Help windows (separate WebView2 popups) from
	// the loop below, so they don't end up hidden behind the main window.
	var topmost atomic.Bool
	setAlwaysOnTop = func(on bool) {
		topmost.Store(on)
		w.Dispatch(func() { setTopmost(hwnd, on); setChildrenTopmost(on) })
	}
	if startAlwaysOnTop {
		topmost.Store(true)
		setTopmost(hwnd, true)
		childTopmost = true
	}
	// Save as dialog for exported pictures and documents (Topology export).
	nativeSaveDialog = func(name, label, ext string) (string, bool) {
		res := make(chan [2]any, 1)
		w.Dispatch(func() {
			p, ok := saveAsDialog(hwnd, name, label, ext)
			res <- [2]any{p, ok}
		})
		select {
		case r := <-res:
			return r[0].(string), r[1].(bool)
		case <-stop:
			return "", false
		}
	}
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		last := ""
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if s := title(); s != last {
					last = s
					w.Dispatch(func() { w.SetTitle(s) })
				}
			}
		}
	}()

	w.Navigate(url)
	w.Run()
	return nil
}

var (
	comdlg32             = windows.NewLazySystemDLL("comdlg32.dll")
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileNameW = comdlg32.NewProc("GetSaveFileNameW")
)

// openFileName mirrors the Win32 OPENFILENAMEW structure.
type openFileName struct {
	structSize      uint32
	owner           uintptr
	instance        uintptr
	filter          *uint16
	customFilter    *uint16
	maxCustomFilter uint32
	filterIndex     uint32
	file            *uint16
	maxFile         uint32
	fileTitle       *uint16
	maxFileTitle    uint32
	initialDir      *uint16
	title           *uint16
	flags           uint32
	fileOffset      uint16
	fileExtension   uint16
	defExt          *uint16
	custData        uintptr
	hook            uintptr
	templateName    *uint16
	reserved        uintptr
	reserved2       uint32
	flagsEx         uint32
}

// utf16z encodes s, which may contain NULs (as dialog filters do), with a
// terminating NUL.
func utf16z(s string) *uint16 {
	u := utf16.Encode([]rune(s + "\x00"))
	return &u[0]
}

// fileDialog shows the Windows Open or Save As dialog for a network or tag
// file. It must run on the UI thread.
func fileDialog(owner uintptr, k FileKind, save bool, current string) (string, bool) {
	const (
		ofnOverwritePrompt = 0x2
		ofnNoChangeDir     = 0x8
		ofnPathMustExist   = 0x800
		ofnFileMustExist   = 0x1000
		ofnExplorer        = 0x80000
	)
	ext := k.DefaultExt()
	label := "Network configuration"
	if k == KindTags {
		label = "Tag configuration"
	}
	filter := fmt.Sprintf("%s (*%s)\x00*%s\x00YAML files (*.yaml;*.yml)\x00*.yaml;*.yml\x00All files (*.*)\x00*.*\x00", label, ext, ext)
	if k == KindTags && !save {
		// A PLC tag table or a data block source is imported as tags (see the page).
		filter = fmt.Sprintf("%s, PLC tag table or block source (*%s;*.xlsx;*.xml;*.sdf;*.scl;*.db;*.udt)\x00*%s;*.xlsx;*.xml;*.sdf;*.scl;*.db;*.udt\x00", label, ext, ext) +
			fmt.Sprintf("%s (*%s)\x00*%s\x00", label, ext, ext) +
			"PLC tag table (*.xlsx;*.xml;*.sdf;*.csv)\x00*.xlsx;*.xml;*.sdf;*.csv\x00" +
			"Data block source (*.scl;*.db;*.udt)\x00*.scl;*.db;*.udt\x00" +
			"YAML files (*.yaml;*.yml)\x00*.yaml;*.yml\x00All files (*.*)\x00*.*\x00"
	}

	buf := make([]uint16, 4096)
	dir, _ := os.Getwd()
	if current != "" {
		dir = filepath.Dir(current)
		if save {
			copy(buf, utf16.Encode([]rune(filepath.Base(current))))
		}
	} else if save {
		copy(buf, utf16.Encode([]rune(map[FileKind]string{KindNetwork: "plant", KindTags: "tags"}[k]+ext)))
	}
	verb := "Open"
	if save {
		verb = "Save"
	}
	ofn := &openFileName{
		owner:       owner,
		filter:      utf16z(filter),
		filterIndex: 1,
		file:        &buf[0],
		maxFile:     uint32(len(buf)),
		initialDir:  utf16z(dir),
		title:       utf16z(verb + " " + strings.ToLower(label)),
		flags:       ofnExplorer | ofnNoChangeDir | ofnPathMustExist,
	}
	ofn.structSize = uint32(unsafe.Sizeof(*ofn))
	proc := procGetOpenFileNameW
	if save {
		proc = procGetSaveFileNameW
		ofn.flags |= ofnOverwritePrompt
	} else {
		ofn.flags |= ofnFileMustExist
	}
	if r, _, _ := proc.Call(uintptr(unsafe.Pointer(ofn))); r == 0 {
		return "", false // cancelled
	}
	path := windows.UTF16ToString(buf)
	if save && filepath.Ext(path) == "" {
		path += ext
	}
	return path, true
}

// saveAsDialog asks where to save a file of one type, starting in Documents.
// It must run on the UI thread.
func saveAsDialog(owner uintptr, name, label, ext string) (string, bool) {
	const ofnOverwritePrompt, ofnNoChangeDir, ofnPathMustExist, ofnExplorer = 0x2, 0x8, 0x800, 0x80000
	buf := make([]uint16, 4096)
	copy(buf, utf16.Encode([]rune(name)))
	dir, _ := windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
	ofn := &openFileName{
		owner:       owner,
		filter:      utf16z(fmt.Sprintf("%s (*%s)\x00*%s\x00All files (*.*)\x00*.*\x00", label, ext, ext)),
		filterIndex: 1,
		file:        &buf[0],
		maxFile:     uint32(len(buf)),
		initialDir:  utf16z(dir),
		title:       utf16z("Save " + label),
		defExt:      utf16z(strings.TrimPrefix(ext, ".")),
		flags:       ofnExplorer | ofnNoChangeDir | ofnPathMustExist | ofnOverwritePrompt,
	}
	ofn.structSize = uint32(unsafe.Sizeof(*ofn))
	if r, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(ofn))); r == 0 {
		return "", false
	}
	return windows.UTF16ToString(buf), true
}

// setWindowIcon sets the title bar and taskbar icon from the embedded PNG.
// (The icon Explorer shows for the .exe itself needs a resource; see README.)
func setWindowIcon(hwnd uintptr) {
	const (
		wmSetIcon            = 0x0080
		iconSmall, iconBig   = 0, 1
		smCxIcon, smCxSmIcon = 11, 49
	)
	for _, k := range []struct{ which, metric uintptr }{{iconBig, smCxIcon}, {iconSmall, smCxSmIcon}} {
		px, _, _ := procGetSystemMetrics.Call(k.metric)
		hicon, _, _ := procCreateIconFromResourceEx.Call(
			uintptr(unsafe.Pointer(&iconPNG[0])), uintptr(len(iconPNG)),
			1, 0x00030000, px, px, 0)
		if hicon != 0 {
			procSendMessageW.Call(hwnd, wmSetIcon, k.which, hicon)
		}
	}
}

var (
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procGetWindowLongPtr = user32.NewProc(longPtrName("GetWindowLong"))
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
)

const (
	wsExTopmost = 0x00000008
	gwlExStyle  = ^uintptr(19) // -20
)

func isTopmost(hwnd uintptr) bool {
	style, _, _ := procGetWindowLongPtr.Call(hwnd, gwlExStyle)
	return style&wsExTopmost != 0
}

// setTopmost keeps hwnd above all normal windows (on) or makes it a normal
// window again, without moving, resizing or activating it.
func setTopmost(hwnd uintptr, on bool) {
	const swpNoSize, swpNoMove, swpNoActivate = 0x1, 0x2, 0x10
	after := ^uintptr(1) // HWND_NOTOPMOST (-2)
	if on {
		after = ^uintptr(0) // HWND_TOPMOST (-1)
	}
	procSetWindowPos.Call(hwnd, after, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoActivate)
}

// syncPopupsTopmost gives the program's other windows (Network settings,
// Topology, Export, Help) the same always-on-top state as the main window.
// They are WebView2 popups hosted by msedgewebview2.exe, recognised by a
// title ending in " – PLC Monitor".
func syncPopupsTopmost(on bool) {
	popupWant.Store(on)
	windows.EnumWindows(popupCallback, nil)
}

// popupCallback is created once: Windows has a fixed number of callback slots.
var (
	popupWant     atomic.Bool
	popupCallback = windows.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		on := popupWant.Load()
		if !windows.IsWindowVisible(h) || isTopmost(uintptr(h)) == on {
			return 1
		}
		buf := make([]uint16, 256)
		n, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if !strings.HasSuffix(windows.UTF16ToString(buf[:n]), " – PLC Monitor") {
			return 1
		}
		var pid uint32
		windows.GetWindowThreadProcessId(h, &pid)
		if p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
			name := make([]uint16, windows.MAX_PATH)
			size := uint32(len(name))
			if windows.QueryFullProcessImageName(p, 0, &name[0], &size) == nil &&
				strings.EqualFold(filepath.Base(windows.UTF16ToString(name[:size])), "msedgewebview2.exe") {
				setTopmost(uintptr(h), on)
			}
			windows.CloseHandle(p)
		}
		return 1
	})
)

// systemDPI returns the display DPI (96 = 100% scaling).
func systemDPI() uintptr {
	if procGetDpiForSystem.Find() != nil {
		return 96
	}
	dpi, _, _ := procGetDpiForSystem.Call()
	if dpi == 0 {
		return 96
	}
	return dpi
}

func showError(msg string) {
	text, _ := windows.UTF16PtrFromString(msg)
	caption, _ := windows.UTF16PtrFromString("PLC Monitor")
	windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
}
