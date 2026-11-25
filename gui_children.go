//go:build gui && windows

package main

// The program's other windows (Network settings, Topology, Export, Help,
// Scan PLC, PLC tags, charts, tag windows, Status) as native windows of
// this program.
//
// A page's window.open would make WebView2 open its own popup window, which
// always shows a read-only address line at the top. Instead, a script added
// to every page replaces window.open and window.close: they send a message
// to this file, which opens a plain window with its own WebView2 inside (no
// address line) or closes one. Windows are found again by the name given to
// window.open, as in a browser.

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// windowScript runs in every page of every window (main and children).
const windowScript = `(() => {
  if (window.__s7win) return;
  window.__s7win = true;
  window.__s7desktop = true;
  window.__s7frame = (op, v) => send({ op, v: v || '' });
  const send = m => {
    const s = JSON.stringify(m);
    if (typeof window.__s7host === 'function') window.__s7host(s);
    else if (window.chrome && window.chrome.webview) window.chrome.webview.postMessage(s);
  };
  const open = window.__s7open = {};
  window.__s7Closed = name => { delete open[name]; };
  window.open = (url, name, features) => {
    name = name || ('w' + Date.now());
    const abs = new URL(url || '', location.href).href;
    open[name] = true;
    send({ op: 'open', url: abs, name, features: features || '' });
    return {
      get closed() { return !open[name]; },
      focus() { send({ op: 'focus', name }); },
      close() { send({ op: 'close', name }); },
      postMessage() {},
    };
  };
  window.close = () => send({ op: 'close' });
  // __s7fit(h): make the window's inside h CSS pixels high (fits a form).
  window.__s7fit = h => send({ op: 'fit', v: String(Math.ceil(h)) });
  // A file dropped where the page does not take it must not be opened or
  // downloaded by WebView2 (pages that take files handle the drop themselves).
  const files = e => [...((e.dataTransfer && e.dataTransfer.types) || [])].includes('Files');
  window.addEventListener('dragover', e => { if (files(e)) e.preventDefault(); });
  window.addEventListener('drop', e => { if (files(e)) e.preventDefault(); });
  const title = () => send({ op: 'title', v: document.title });
  document.addEventListener('DOMContentLoaded', () => {
    title();
    const t = document.querySelector('title');
    if (t) new MutationObserver(title).observe(t, { childList: true, characterData: true, subtree: true });
  });
})();`

type childWindow struct {
	hwnd     uintptr
	name     string
	url      string
	chromium *edge.Chromium
}

var (
	mainView     webview2.WebView
	mainHwnd     uintptr
	children     = map[uintptr]*childWindow{} // UI thread only
	childByName  = map[string]*childWindow{}
	childClass   *uint16
	childTopmost bool

	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSetWindowTextW      = user32.NewProc("SetWindowTextW")
	procIsIconic            = user32.NewProc("IsIconic")
	procPostMessageW        = user32.NewProc("PostMessageW")
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

// setupWindows prepares the main window: its pages get the window script,
// and their messages come back through a binding.
func setupWindows(w webview2.WebView) {
	mainView, mainHwnd = w, uintptr(w.Window())
	w.Bind("__s7host", func(msg string) {
		// Bindings run inside a WebView2 callback: do the work afterwards.
		w.Dispatch(func() { handleWindowMessage(0, msg) })
	})
	w.Init(windowScript)
}

type windowMsg struct {
	Op       string `json:"op"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	Features string `json:"features"`
	V        string `json:"v"`
}

// handleWindowMessage acts on a message from the main window (from = 0) or
// a child window. It runs on the UI thread.
func handleWindowMessage(from uintptr, raw string) {
	var m windowMsg
	if json.Unmarshal([]byte(raw), &m) != nil {
		return
	}
	switch m.Op {
	case "open":
		openChild(m.URL, m.Name, m.Features)
	case "focus":
		if c := childByName[m.Name]; c != nil {
			focusWindow(c.hwnd)
		}
	case "close":
		target := from
		if m.Name != "" {
			target = 0
			if c := childByName[m.Name]; c != nil {
				target = c.hwnd
			}
		}
		if target != 0 { // the main window is closed with its own close button or File → Exit
			procPostMessageW.Call(target, 0x0010, 0, 0) // WM_CLOSE
		}
	case "min", "max", "quit", "drag", "resize", "framestate":
		h := from
		if h == 0 {
			h = mainHwnd
		}
		frameAction(h, m.Op)
	case "zoom": // View → Zoom, the − / + in the menu bar: "+", "-", "0" (100 %), "?" (report)
		zoomStep(m.V)
	case "fit":
		if from != 0 {
			if h, err := strconv.Atoi(m.V); err == nil && h >= 100 {
				fitHeight(from, h)
			}
		}
	case "title":
		if from != 0 && m.V != "" {
			t, _ := windows.UTF16PtrFromString(m.V)
			procSetWindowTextW.Call(from, uintptr(unsafe.Pointer(t)))
		}
	}
}

func focusWindow(hwnd uintptr) {
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, 9) // SW_RESTORE
	}
	procSetForegroundWindow.Call(hwnd)
}

var featureRe = regexp.MustCompile(`(width|height)\s*=\s*(\d+)`)

// openChild opens url in the window called name, or in a new window.
func openChild(url, name, features string) {
	if c := childByName[name]; c != nil {
		if url != c.url {
			c.url = url
			c.chromium.Navigate(url)
		}
		focusWindow(c.hwnd)
		return
	}
	width, height := 900, 700
	for _, f := range featureRe.FindAllStringSubmatch(features, -1) {
		n, _ := strconv.Atoi(f[2])
		if n >= 200 && n <= 4000 {
			if f[1] == "width" {
				width = n
			} else {
				height = n
			}
		}
	}
	dpi := int(systemDPI())
	width, height = width*dpi/96, height*dpi/96

	var inst windows.Handle
	windows.GetModuleHandleEx(0, nil, &inst)
	if childClass == nil {
		childClass, _ = windows.UTF16PtrFromString("PLCMonitorWindow")
		cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512) // IDC_ARROW
		wc := wndClassEx{wndProc: childProcCallback, instance: inst, className: childClass, cursor: windows.Handle(cursor)}
		wc.size = uint32(unsafe.Sizeof(wc))
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	}
	title, _ := windows.UTF16PtrFromString("PLC Monitor")
	const wsOverlappedWindow, cwUseDefault = 0x00CF0000, 0x80000000
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(childClass)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow, cwUseDefault, cwUseDefault, uintptr(width), uintptr(height), 0, 0, uintptr(inst), 0)
	if hwnd == 0 {
		log.Printf("window %s: %v", name, err)
		return
	}
	setWindowIcon(hwnd)
	const swpNoSize, swpNoMove, swpNoZOrder, swpFrameChanged = 0x1, 0x2, 0x4, 0x20
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoZOrder|swpFrameChanged)
	if childTopmost {
		setTopmost(hwnd, true)
	}
	c := &childWindow{hwnd: hwnd, name: name, url: url}
	children[hwnd] = c
	childByName[name] = c
	procShowWindow.Call(hwnd, 5) // SW_SHOW

	ch := edge.NewChromium()
	ch.DataPath = filepath.Join(os.Getenv("LOCALAPPDATA"), "PLCMonitor", "WebView2")
	ch.SetPermission(edge.CoreWebView2PermissionKindClipboardRead, edge.CoreWebView2PermissionStateAllow)
	ch.MessageCallback = func(msg string) {
		mainView.Dispatch(func() { handleWindowMessage(hwnd, msg) })
	}
	c.chromium = ch
	if !ch.Embed(hwnd) {
		log.Printf("window %s: WebView2 could not start", name)
		procDestroyWindow.Call(hwnd)
		return
	}
	if s, err := ch.GetSettings(); err == nil {
		s.PutAreDefaultContextMenusEnabled(false)
		s.PutAreDevToolsEnabled(false)
		s.PutIsStatusBarEnabled(false)
	}
	ch.Resize()
	if appZoom != 1 { // the same zoom as the main window
		putZoom(ch.GetController(), appZoom)
	}
	ch.Init(windowScript)
	ch.Init(childBarScript)
	ch.Navigate(url)
	ch.Focus()
	// Embed ran its own message loop; make sure dispatched work that
	// arrived meanwhile is run.
	mainView.Dispatch(func() {})
}

var (
	procGetWindowRect = user32.NewProc("GetWindowRect")
	procGetClientRect = user32.NewProc("GetClientRect")
)

type rect struct{ left, top, right, bottom int32 }

// fitHeight makes the inside of a window h CSS pixels high, keeping its width
// and position, and no taller than the screen.
func fitHeight(hwnd uintptr, h int) {
	var wr, cr rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr)))
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&cr)))
	frame := int(wr.bottom-wr.top) - int(cr.bottom-cr.top)
	outer := h*int(systemDPI())/96 + frame
	screen, _, _ := procGetSystemMetrics.Call(17) // SM_CYFULLSCREEN: screen minus the taskbar
	if s := int(screen); s > 200 && outer > s {
		outer = s
	}
	const swpNoMove, swpNoZOrder, swpNoActivate = 0x2, 0x4, 0x10
	procSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(wr.right-wr.left), uintptr(outer), swpNoMove|swpNoZOrder|swpNoActivate)
}

// setChildrenTopmost applies View → Always on top to the child windows.
func setChildrenTopmost(on bool) {
	childTopmost = on
	for h := range children {
		setTopmost(h, on)
	}
}

var childProcCallback = windows.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
	const (
		wmDestroy, wmSize, wmMove, wmActivate, wmClose, wmMoving = 0x0002, 0x0005, 0x0003, 0x0006, 0x0010, 0x0216
	)
	c := children[hwnd]
	switch msg {
	case 0x0083: // WM_NCCALCSIZE: no caption, the page draws the title bar
		if wp != 0 {
			return noCaption(hwnd, wp, lp)
		}
	case wmSize:
		if c != nil && c.chromium != nil {
			c.chromium.Resize()
			tellMaximized(hwnd)
		}
	case wmMove, wmMoving:
		if c != nil && c.chromium != nil {
			c.chromium.NotifyParentWindowPositionChanged()
		}
	case wmActivate:
		if c != nil && c.chromium != nil && wp&0xFFFF != 0 {
			c.chromium.Focus()
		}
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if c != nil {
			if c.chromium != nil {
				c.chromium.Navigate("about:blank") // let go of the page
			}
			delete(children, hwnd)
			if childByName[c.name] == c {
				delete(childByName, c.name)
			}
			// Other windows learn that this one is closed (win.closed).
			name, _ := json.Marshal(c.name)
			js := "window.__s7Closed && window.__s7Closed(" + string(name) + ")"
			mainView.Eval(js)
			for _, o := range children {
				if o.chromium != nil {
					o.chromium.Eval(js)
				}
			}
		}
		return 0 // unlike the main window, closing this one does not end the program
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
})

// ---- the main window's own title bar ----
//
// The main window has no Windows caption: the page draws its title bar
// (icon, name, link state, settings, minimise / maximise / close). The
// resize borders stay, and the top edge is resized from the page's top
// pixels. Dragging the title bar moves the window as usual, including Snap.

var (
	procSetWindowLongPtrW = user32.NewProc(longPtrName("SetWindowLong"))
	procCallWindowProcW   = user32.NewProc("CallWindowProcW")
	procReleaseCapture    = user32.NewProc("ReleaseCapture")
	procIsZoomed          = user32.NewProc("IsZoomed")
	mainOrigProc          uintptr
)

const gwlpWndProc = ^uintptr(3) // -4

func customFrame(hwnd uintptr) {
	mainOrigProc, _, _ = procGetWindowLongPtr.Call(hwnd, gwlpWndProc)
	procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, mainFrameProc)
	const swpNoSize, swpNoMove, swpNoZOrder, swpFrameChanged = 0x1, 0x2, 0x4, 0x20
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoZOrder|swpFrameChanged)
}

func isZoomed(hwnd uintptr) bool { r, _, _ := procIsZoomed.Call(hwnd); return r != 0 }

// tellMaximized lets a page show the maximise or the restore button.
func tellMaximized(h uintptr) {
	js := "window.__s7maxed && window.__s7maxed(" + strconv.FormatBool(isZoomed(h)) + ")"
	if h == mainHwnd && mainView != nil {
		mainView.Eval(js)
	} else if c := children[h]; c != nil && c.chromium != nil {
		c.chromium.Eval(js)
	}
}

var mainFrameProc = windows.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
	const wmNcCalcSize, wmSize, wmDestroy = 0x0083, 0x0005, 0x0002
	switch msg {
	case wmNcCalcSize:
		if wp != 0 {
			return noCaption(hwnd, wp, lp)
		}
	case wmSize:
		defer tellMaximized(hwnd)
	case wmDestroy:
		saveWindowState(hwnd) // size, position, zoom for the next start
	}
	r, _, _ := procCallWindowProcW.Call(mainOrigProc, hwnd, msg, wp, lp)
	return r
})

// noCaption answers WM_NCCALCSIZE for a window without a caption: Windows
// works out the borders, then the caption is taken away.
func noCaption(hwnd, wp, lp uintptr) uintptr {
	r := *(**rect)(unsafe.Pointer(&lp)) // lp points to NCCALCSIZE_PARAMS, whose first field is the rect
	top := r.top
	ret, _, _ := procDefWindowProcW.Call(hwnd, 0x0083, wp, lp)
	r.top = top
	if isZoomed(hwnd) {
		// A maximised window sticks out of the screen by its frame.
		fy, _, _ := procGetSystemMetrics.Call(33)  // SM_CYFRAME
		pad, _, _ := procGetSystemMetrics.Call(92) // SM_CXPADDEDBORDER
		r.top += int32(fy + pad)
	}
	return ret
}

// frameAction carries out a title bar button or drag from a page.
func frameAction(h uintptr, op string) {
	const wmNcLButtonDown, htCaption, htTop, wmClose = 0x00A1, 2, 12, 0x0010
	switch op {
	case "min":
		procShowWindow.Call(h, 6) // SW_MINIMIZE
	case "max":
		if isZoomed(h) {
			procShowWindow.Call(h, 9) // SW_RESTORE
		} else {
			procShowWindow.Call(h, 3) // SW_MAXIMIZE
		}
	case "quit":
		procPostMessageW.Call(h, wmClose, 0, 0)
	case "drag", "resize":
		ht := uintptr(htCaption)
		if op == "resize" {
			if isZoomed(h) {
				return
			}
			ht = htTop
		}
		procReleaseCapture.Call()
		procPostMessageW.Call(h, wmNcLButtonDown, ht, 0)
	case "framestate":
		tellMaximized(h)
	}
}

// childBarScript draws the title bar of the other windows, matching the main
// window: icon, the page title, minimise / maximise / close.
const childBarScript = `(() => {
  if (window.__s7bar) return;
  window.__s7bar = true;
  const H = 32, frame = (op) => window.__s7frame && window.__s7frame(op);
  const css = '#s7tb{position:fixed;top:0;left:0;right:0;height:' + H + 'px;z-index:2147483000;display:flex;align-items:stretch;' +
    'background:var(--card,#fff);color:var(--fg,#1d2330);border-bottom:1px solid var(--line,#e5e7eb);' +
    'font:12px "Segoe UI",system-ui,sans-serif;user-select:none;box-sizing:border-box}' +
    '#s7tb .r{position:absolute;left:0;right:0;top:0;height:4px;cursor:ns-resize}' +
    '#s7tb .t{flex:1;display:flex;align-items:center;gap:8px;padding:0 10px;min-width:0;overflow:hidden}' +
    '#s7tb .t span{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}' +
    '#s7tb img{width:16px;height:16px;flex:none}' +
    '#s7tb button{width:46px;flex:none;border:0;border-radius:0;background:none;padding:0;margin:0;color:inherit;cursor:default;' +
    'font:10px "Segoe Fluent Icons","Segoe MDL2 Assets",sans-serif;display:flex;align-items:center;justify-content:center}' +
    '#s7tb button:hover{background:color-mix(in srgb,currentColor 10%,transparent)}' +
    '#s7tb button.x:hover{background:#c42b1c;color:#fff}' +
    'html{--s7tb:' + H + 'px}body{padding-top:' + H + 'px!important;box-sizing:border-box}';
  const add = () => {
    if (!document.body || document.getElementById('s7tb')) return;
    const st = document.createElement('style');
    st.textContent = css;
    document.head.appendChild(st);
    const bar = document.createElement('div');
    bar.id = 's7tb';
    bar.innerHTML = '<div class="r"></div><div class="t"><img src="/favicon.png" alt=""><span></span></div>' +
      '<button class="m" title="Minimize"></button><button class="mx" title="Maximize"></button>' +
      '<button class="x" title="Close"></button>';
    document.body.prepend(bar);
    const title = bar.querySelector('.t span');
    const setTitle = () => { title.textContent = document.title || 'PLC Monitor'; };
    setTitle();
    const t = document.querySelector('title');
    if (t) new MutationObserver(setTitle).observe(t, { childList: true, characterData: true, subtree: true });
    bar.addEventListener('mousedown', ev => {
      if (ev.button !== 0 || ev.target.closest('button')) return;
      if (ev.target.classList.contains('r')) { frame('resize'); return; }
      if (ev.detail === 2) return;
      frame('drag');
    });
    bar.addEventListener('dblclick', ev => { if (!ev.target.closest('button')) frame('max'); });
    bar.querySelector('.m').onclick = () => frame('min');
    bar.querySelector('.mx').onclick = () => frame('max');
    bar.querySelector('.x').onclick = () => frame('quit');
    window.__s7maxed = on => {
      const b = bar.querySelector('.mx');
      b.textContent = on ? '' : '';
      b.title = on ? 'Restore' : 'Maximize';
      bar.querySelector('.r').style.display = on ? 'none' : '';
    };
    frame('framestate');
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', add); else add();
})();`

// longPtrName: 64-bit Windows has GetWindowLongPtrW / SetWindowLongPtrW;
// on 32-bit they are only C macros for GetWindowLongW / SetWindowLongW.
func longPtrName(base string) string {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return base + "PtrW"
	}
	return base + "W"
}
