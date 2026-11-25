package tray

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmUser          = 0x0400
	wmTrayCallback  = wmUser + 20
	wmCommand       = 0x0111
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	mfString     = 0x00000000
	mfSeparator  = 0x00000800
	tpmRightBtn  = 0x0002
	tpmBottomAlg = 0x0020

	swHide    = 0
	swRestore = 9

	gwlpWndProc = ^uintptr(3) // -4 as uintptr on Win64
)

const (
	MenuShowWindow       = 1001
	MenuLaunchAntigrav   = 1002
	MenuLaunchCursor     = 1003
	MenuApplyAll         = 1004
	MenuRestoreAll       = 1005
	MenuQuit             = 1009
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procCreatePopupMenu      = user32.NewProc("CreatePopupMenu")
	procAppendMenuW          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu       = user32.NewProc("TrackPopupMenu")
	procDestroyMenu          = user32.NewProc("DestroyMenu")
	procGetCursorPos         = user32.NewProc("GetCursorPos")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")
	procSetWindowLongPtrW    = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW      = user32.NewProc("CallWindowProcW")
	procCreateIconIndirect   = user32.NewProc("CreateIconIndirect")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procShellNotifyIconW     = shell32.NewProc("Shell_NotifyIconW")
	procCreateBitmap         = gdi32.NewProc("CreateBitmap")
	procCreateDIBSection     = gdi32.NewProc("CreateDIBSection")
	procDeleteObject         = gdi32.NewProc("DeleteObject")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
)

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type point struct {
	X int32
	Y int32
}

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type notifyIconDataW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

type Manager struct {
	trayHwnd    uintptr
	mainHwnd    uintptr
	origWndProc uintptr
	hIcon       uintptr
	onAction    func(menuID int)
	shouldHide  func() bool
	quitting    bool
}

var globalMgr *Manager

func NewManager(onAction func(menuID int), shouldHideOnClose func() bool) *Manager {
	m := &Manager{
		onAction:   onAction,
		shouldHide: shouldHideOnClose,
	}
	globalMgr = m
	return m
}

// Start launches the Win32 system tray icon and message pump on its own OS thread.
func (m *Manager) Start(tooltip string) {
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hInst, _, _ := procGetModuleHandleW.Call(0)
		clsName, _ := windows.UTF16PtrFromString("ProxyBoxTrayClass")

		wc := wndClassExW{
			CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			LpfnWndProc:   syscall.NewCallback(trayWndProc),
			HInstance:     hInst,
			LpszClassName: clsName,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

		hwnd, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(clsName)),
			uintptr(unsafe.Pointer(clsName)),
			0, 0, 0, 0, 0,
			0, 0, hInst, 0,
		)
		m.trayHwnd = hwnd
		m.hIcon = createAppIcon()

		var nid notifyIconDataW
		nid.CbSize = uint32(unsafe.Sizeof(nid))
		nid.HWnd = hwnd
		nid.UID = 1
		nid.UFlags = nifMessage | nifIcon | nifTip
		nid.UCallbackMessage = wmTrayCallback
		nid.HIcon = m.hIcon
		copyTip(&nid.SzTip, tooltip)

		procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))

		var message msg
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
}

// AttachMainWindow sets the custom icon on the WebView2 window and intercepts WM_CLOSE
// so clicking [X] hides to tray when MinimizeToTray is enabled.
func (m *Manager) AttachMainWindow(hwnd uintptr) {
	m.mainHwnd = hwnd
	if hwnd == 0 {
		return
	}
	if m.hIcon == 0 {
		m.hIcon = createAppIcon()
	}
	const wmSetIcon = 0x0080
	procSendMessageW.Call(hwnd, wmSetIcon, 0, m.hIcon)
	procSendMessageW.Call(hwnd, wmSetIcon, 1, m.hIcon)

	cb := syscall.NewCallback(mainSubclassWndProc)
	orig, _, _ := procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, cb)
	m.origWndProc = orig
}

func (m *Manager) ShowMainWindow() {
	if m.mainHwnd == 0 {
		return
	}
	procShowWindow.Call(m.mainHwnd, swRestore)
	procSetForegroundWindow.Call(m.mainHwnd)
}

func (m *Manager) HideMainWindow() {
	if m.mainHwnd == 0 {
		return
	}
	procShowWindow.Call(m.mainHwnd, swHide)
}

func (m *Manager) NotifyBalloon(title, info string) {
	if m.trayHwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = m.trayHwnd
	nid.UID = 1
	nid.UFlags = nifInfo
	nid.DwInfoFlags = 0x00000001 // NIIF_INFO
	copyUTF16(nid.SzInfoTitle[:], title)
	copyUTF16(nid.SzInfo[:], info)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (m *Manager) Cleanup() {
	m.quitting = true
	if m.trayHwnd != 0 {
		var nid notifyIconDataW
		nid.CbSize = uint32(unsafe.Sizeof(nid))
		nid.HWnd = m.trayHwnd
		nid.UID = 1
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
		procPostMessageW.Call(m.trayHwnd, wmDestroy, 0, 0)
	}
}

func mainSubclassWndProc(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
	if globalMgr != nil && uMsg == wmClose && !globalMgr.quitting {
		if globalMgr.shouldHide != nil && globalMgr.shouldHide() {
			procShowWindow.Call(hwnd, swHide)
			globalMgr.NotifyBalloon("Proxy Box 仍在后台运行", "双击托盘图标可重新打开面板，右键可快速免 TUN 启动 IDE")
			return 0
		}
	}
	if globalMgr != nil && globalMgr.origWndProc != 0 {
		ret, _, _ := procCallWindowProcW.Call(globalMgr.origWndProc, hwnd, uintptr(uMsg), wParam, lParam)
		return ret
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
	return ret
}

func trayWndProc(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
	switch uMsg {
	case wmTrayCallback:
		switch uint32(lParam) {
		case wmLButtonUp, wmLButtonDblClk:
			if globalMgr != nil {
				globalMgr.ShowMainWindow()
			}
			return 0
		case wmRButtonUp:
			showContextMenu(hwnd)
			return 0
		}
	case wmCommand:
		id := int(wParam & 0xffff)
		if globalMgr != nil && globalMgr.onAction != nil {
			go globalMgr.onAction(id)
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
	return ret
}

func showContextMenu(hwnd uintptr) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	appendItem := func(id uintptr, text string) {
		ptr, _ := windows.UTF16PtrFromString(text)
		procAppendMenuW.Call(hMenu, mfString, id, uintptr(unsafe.Pointer(ptr)))
	}
	appendSep := func() {
		procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
	}

	appendItem(MenuShowWindow, "打开 Proxy Box 控制台")
	appendSep()
	appendItem(MenuLaunchAntigrav, "免 TUN 代理启动 Antigravity")
	appendItem(MenuLaunchCursor, "免 TUN 代理启动 Cursor")
	appendSep()
	appendItem(MenuApplyAll, "一键应用免 TUN 配置")
	appendItem(MenuRestoreAll, "一键还原默认配置")
	appendSep()
	appendItem(MenuQuit, "彻底退出")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	procTrackPopupMenu.Call(hMenu, tpmRightBtn|tpmBottomAlg, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
}

func copyTip(dst *[128]uint16, s string) {
	copyUTF16(dst[:], s)
}

func copyUTF16(dst []uint16, s string) {
	u := windows.StringToUTF16(s)
	n := len(u)
	if n > len(dst)-1 {
		n = len(dst) - 1
	}
	copy(dst[:n], u[:n])
	dst[n] = 0
}

// createAppIcon renders a crisp 32x32 32-bit ARGB rounded gradient badge icon in memory.
func createAppIcon() uintptr {
	const size = 32
	bih := bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       size,
		BiHeight:      -size, // top-down DIB
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: 0, // BI_RGB
	}
	var bits unsafe.Pointer
	hColor, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bih)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hColor == 0 || bits == nil {
		return 0
	}

	pixels := unsafe.Slice((*uint32)(bits), size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// Rounded rect check (radius 7)
			if !insideRoundedRect(x, y, size, size, 7) {
				pixels[y*size+x] = 0x00000000
				continue
			}
			// Forest-emerald diagonal gradient (#1A4B33 -> #103222) matching brand logo
			t := float64(x+y) / float64(2*size)
			r := uint32(26*(1-t) + 16*t)
			g := uint32(75*(1-t) + 50*t)
			b := uint32(51*(1-t) + 34*t)
			bg := (uint32(0xFF) << 24) | (r << 16) | (g << 8) | b

			// Draw lime-green (#C4F269) lightning glyph in center
			if isLightningPixel(x, y) {
				pixels[y*size+x] = 0xFFC4F269
			} else {
				pixels[y*size+x] = bg
			}
		}
	}

	maskBytes := make([]byte, (size*size)/8)
	hMask, _, _ := procCreateBitmap.Call(size, size, 1, 1, uintptr(unsafe.Pointer(&maskBytes[0])))

	ii := iconInfo{
		FIcon:    1,
		HbmMask:  hMask,
		HbmColor: hColor,
	}
	hIcon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	if hMask != 0 {
		procDeleteObject.Call(hMask)
	}
	if hColor != 0 {
		procDeleteObject.Call(hColor)
	}
	return hIcon
}

func insideRoundedRect(x, y, w, h, rad int) bool {
	if x < 1 || y < 1 || x >= w-1 || y >= h-1 {
		return false
	}
	cx, cy := x, y
	if x < rad+1 {
		cx = rad + 1
	} else if x > w-rad-2 {
		cx = w - rad - 2
	}
	if y < rad+1 {
		cy = rad + 1
	} else if y > h-rad-2 {
		cy = h - rad - 2
	}
	dx := x - cx
	dy := y - cy
	return dx*dx+dy*dy <= rad*rad
}

func isLightningPixel(x, y int) bool {
	// Stylized bolt polygon inside 32x32 icon
	// Upper half (y: 6..17): slanted bar from (18,6) down-left to (9,17)
	if y >= 6 && y <= 17 {
		left := 18 - (y-6)*9/11
		right := left + 5
		if y >= 15 {
			right = 22
		}
		if x >= left && x <= right {
			return true
		}
	}
	// Lower half (y: 15..25): slanted bar from (14,15) down-left to (13,25)
	if y >= 15 && y <= 25 {
		right := 22 - (y-15)*9/10
		left := right - 5
		if y <= 17 {
			left = 10
		}
		if x >= left && x <= right {
			return true
		}
	}
	return false
}
