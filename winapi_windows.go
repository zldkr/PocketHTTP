package main

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var gdi32 = syscall.NewLazyDLL("gdi32.dll")
var comctl32 = syscall.NewLazyDLL("comctl32.dll")
var comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
var createWindow = user32.NewProc("CreateWindowExW")
var defWindow = user32.NewProc("DefWindowProcW")
var sendMsg = user32.NewProc("SendMessageW")
var postMsg = user32.NewProc("PostMessageW")
var destroyWindow = user32.NewProc("DestroyWindow")
var setTextProc = user32.NewProc("SetWindowTextW")
var getTextLength = user32.NewProc("GetWindowTextLengthW")
var getTextProc = user32.NewProc("GetWindowTextW")
var showWindow = user32.NewProc("ShowWindow")
var enableWindow = user32.NewProc("EnableWindow")
var moveWindow = user32.NewProc("MoveWindow")
var setFocus = user32.NewProc("SetFocus")
var getFocus = user32.NewProc("GetFocus")
var getClientRect = user32.NewProc("GetClientRect")
var setTimer = user32.NewProc("SetTimer")
var killTimer = user32.NewProc("KillTimer")
var messageBoxProc = user32.NewProc("MessageBoxW")
var font, mono, instance uintptr
var scale = 1.0

const (
	wmCreate       = 0x0001
	wmDestroy      = 0x0002
	wmSize         = 0x0005
	wmClose        = 0x0010
	wmCommand      = 0x0111
	wmNotify       = 0x004e
	wmTimer        = 0x0113
	wmGetMinMax    = 0x0024
	wmSetFont      = 0x0030
	wmKeyDown      = 0x0100
	wmRequestDone  = 0x8001
	wmSaveDone     = 0x8002
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsBorder       = 0x00800000
	wsVScroll      = 0x00200000
	wsHScroll      = 0x00100000
	esMultiline    = 0x0004
	esAutoVScroll  = 0x0040
	esAutoHScroll  = 0x0080
	esReadOnly     = 0x0800
	esPassword     = 0x0020
	esWantReturn   = 0x1000
	cbAddString    = 0x0143
	cbGetCurSel    = 0x0147
	cbSetCurSel    = 0x014e
	cbResetContent = 0x014b
	lbAddString    = 0x0180
	lbResetContent = 0x0184
	lbGetCurSel    = 0x0188
	tvInsert       = 0x1132
	tvDelete       = 0x1101
	tvGetNext      = 0x110a
	tvSelect       = 0x110b
	tvExpand       = 0x1102
	tvGetItem      = 0x113e
	tcInsert       = 0x133e
	tcGetCurSel    = 0x130b
	tcSetCurSel    = 0x130c
	emSetLimit     = 0x00c5
	emSetSel       = 0x00b1
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type msg struct {
	Hwnd           uintptr
	Message        uint32
	Wparam, Lparam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}
type wndClass struct {
	Size, Style                        uint32
	Callback                           uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}
type minMaxInfo struct{ Reserved, MaxSize, MaxPosition, MinTrack, MaxTrack point }
type nmhdr struct {
	Hwnd, ID uintptr
	Code     int32
}
type tvItem struct {
	Mask                                    uint32
	Item                                    uintptr
	State, StateMask                        uint32
	Text                                    *uint16
	TextMax, Image, SelectedImage, Children int32
	Param                                   uintptr
}
type tvInsertStruct struct {
	Parent, After uintptr
	Item          tvItem
}
type tcItem struct {
	Mask, State, StateMask uint32
	Text                   *uint16
	TextMax, Image         int32
	Param                  uintptr
}
type openFileName struct {
	Size                         uint32
	Owner, Instance              uintptr
	Filter, CustomFilter         *uint16
	MaxCustomFilter, FilterIndex uint32
	File                         *uint16
	MaxFile                      uint32
	FileTitle                    *uint16
	MaxFileTitle                 uint32
	InitialDir, Title            *uint16
	Flags                        uint32
	FileOffset, FileExtension    uint16
	DefExt                       *uint16
	Data, Hook                   uintptr
	TemplateName                 *uint16
	Reserved                     uintptr
	Reserved2, FlagsEx           uint32
}

func utf(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", "�"))
	return p
}
func text(hwnd uintptr) string {
	n, _, _ := getTextLength.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	getTextProc.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}
func setText(hwnd uintptr, s string) {
	p := utf(s)
	setTextProc.Call(hwnd, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}
func send(hwnd uintptr, message uint32, w, l uintptr) uintptr {
	v, _, _ := sendMsg.Call(hwnd, uintptr(message), w, l)
	return v
}
func control(parent uintptr, class, title string, style uint32, id int) uintptr {
	c, t := utf(class), utf(title)
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), uintptr(wsChild|wsVisible|style), 0, 0, 0, 0, parent, uintptr(id), instance, 0)
	if hwnd == 0 {
		panic(fmt.Sprint("CreateWindow ", class, ": ", err))
	}
	send(hwnd, wmSetFont, font, 1)
	if class == "EDIT" {
		send(hwnd, emSetLimit, 2<<20, 0)
	}
	return hwnd
}
func move(hwnd uintptr, x, y, w, h int) {
	moveWindow.Call(hwnd, uintptr(px(x)), uintptr(px(y)), uintptr(px(max(0, w))), uintptr(px(max(0, h))), 1)
}
func px(v int) int { return int(float64(v)*scale + .5) }
func visible(hwnd uintptr, show bool) {
	v := uintptr(0)
	if show {
		v = 5
	}
	showWindow.Call(hwnd, v)
}
func enabled(hwnd uintptr, value bool) {
	v := uintptr(0)
	if value {
		v = 1
	}
	enableWindow.Call(hwnd, v)
}
func addCombo(hwnd uintptr, items []string) {
	send(hwnd, cbResetContent, 0, 0)
	for _, s := range items {
		p := utf(s)
		send(hwnd, cbAddString, 0, uintptr(unsafe.Pointer(p)))
		runtime.KeepAlive(p)
	}
	send(hwnd, cbSetCurSel, 0, 0)
}
func comboIndex(hwnd uintptr) int  { return int(int32(send(hwnd, cbGetCurSel, 0, 0))) }
func setCombo(hwnd uintptr, i int) { send(hwnd, cbSetCurSel, uintptr(i), 0) }
func checked(hwnd uintptr) bool    { return send(hwnd, 0x00f0, 0, 0) == 1 }
func setChecked(hwnd uintptr, v bool) {
	b := uintptr(0)
	if v {
		b = 1
	}
	send(hwnd, 0x00f1, b, 0)
}
func addTab(hwnd uintptr, label string, i int) {
	p := utf(label)
	item := tcItem{Mask: 1, Text: p}
	send(hwnd, tcInsert, uintptr(i), uintptr(unsafe.Pointer(&item)))
	runtime.KeepAlive(p)
}
func message(owner uintptr, title, body string, flags uintptr) int {
	t, b := utf(title), utf(body)
	r, _, _ := messageBoxProc.Call(owner, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), flags)
	runtime.KeepAlive(t)
	runtime.KeepAlive(b)
	return int(r)
}
func fileDialog(owner uintptr, save bool, title, name, ext string) (string, bool) {
	buffer := make([]uint16, 32768)
	copy(buffer, syscall.StringToUTF16(name))
	filter := syscall.StringToUTF16("JSON / 文本 / 所有文件")
	filter = append(filter, syscall.StringToUTF16("*.*")...)
	filter = append(filter, 0)
	caption, extension := utf(title), utf(ext)
	ofn := openFileName{Owner: owner, Filter: &filter[0], FilterIndex: 1, File: &buffer[0], MaxFile: uint32(len(buffer)), Title: caption, DefExt: extension, Flags: 0x80000 | 0x800 | 0x8}
	ofn.Size = uint32(unsafe.Sizeof(ofn))
	proc := comdlg32.NewProc("GetOpenFileNameW")
	if save {
		proc = comdlg32.NewProc("GetSaveFileNameW")
		ofn.Flags |= 2
	} else {
		ofn.Flags |= 0x1000
	}
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(filter)
	runtime.KeepAlive(caption)
	runtime.KeepAlive(extension)
	if ok == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buffer), true
}
func clipboard(owner uintptr, value string) error {
	buf := syscall.StringToUTF16(strings.ReplaceAll(value, "\x00", "�"))
	handle, _, err := kernel32.NewProc("GlobalAlloc").Call(2, uintptr(len(buf)*2))
	if handle == 0 {
		return err
	}
	pointer, _, err := kernel32.NewProc("GlobalLock").Call(handle)
	if pointer == 0 {
		kernel32.NewProc("GlobalFree").Call(handle)
		return err
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(pointer)), len(buf)), buf)
	kernel32.NewProc("GlobalUnlock").Call(handle)
	opened, _, err := user32.NewProc("OpenClipboard").Call(owner)
	if opened == 0 {
		kernel32.NewProc("GlobalFree").Call(handle)
		return err
	}
	defer user32.NewProc("CloseClipboard").Call()
	user32.NewProc("EmptyClipboard").Call()
	ok, _, err := user32.NewProc("SetClipboardData").Call(13, handle)
	if ok == 0 {
		kernel32.NewProc("GlobalFree").Call(handle)
		return err
	}
	return nil
}
