package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

const (
	idNew = 1001 + iota
	idNewCollection
	idNewFolder
	idEdit
	idDelete
	idImport
	idExport
	idClearHistory
	idTree
	idHistory
	idMethod
	idURL
	idSend
	idCancel
	idSave
	idCurl
	idRequestTabs
	idHeaders
	idAuthType
	idToken
	idUsername
	idPassword
	idKey
	idValue
	idAuthIn
	idShowSecret
	idBodyType
	idFormat
	idBody
	idTimeout
	idFollow
	idInsecure
	idResponseTabs
	idOutput
	idPretty
	idCopy
	idDownload
	idHelp
	idQuit
)
const (
	idDialogName   = 2001
	idDialogParent = 2002
	idDialogOK     = 2003
	idDialogCancel = 2004
)

type saveJob struct {
	data []byte
	seq  uint64
	quit bool
}
type saveResult struct {
	seq  uint64
	quit bool
	err  error
}
type requestResult struct {
	response *Response
	err      error
}
type application struct {
	hwnd                           uintptr
	w                              Workspace
	disk                           *store
	controls                       map[int]uintptr
	labels                         map[string]uintptr
	pages                          [4][]uintptr
	secretControls                 []uintptr
	authGroups                     [4][]uintptr
	name, status, responseStatus   uintptr
	treeSelection                  string
	treeIDs                        map[uintptr]string
	treeHandles                    map[string]uintptr
	loading, buildingTree, closing bool
	seq                            uint64
	cancel                         context.CancelFunc
	last                           *Response
	pretty                         bool
	jobs                           chan saveJob
	saves                          chan saveResult
	requests                       chan requestResult
	editor                         *nodeEditor
	mutex                          uintptr
}

var app *application
var nativeReady chan uintptr
var nativeClosed chan struct{}

func main() { runApp() }
func runApp() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	debug.SetMemoryLimit(32 << 20)
	debug.SetGCPercent(50)
	dataPath, err := workspacePath()
	if err != nil {
		message(0, "Pocket HTTP", err.Error(), 0x10)
		return
	}
	identity := fmt.Sprintf("Local\\PocketHTTP-Native-%x", sha256.Sum256([]byte(dataPath)))
	p := utf(identity)
	mutex, _, mutexErr := kernel32.NewProc("CreateMutexW").Call(0, 1, uintptr(unsafe.Pointer(p)))
	if mutexErr == syscall.Errno(183) {
		existing, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(utf("PocketHTTPNativeWindow"))), 0)
		if existing != 0 {
			showWindow.Call(existing, 9)
			user32.NewProc("SetForegroundWindow").Call(existing)
		}
		if mutex != 0 {
			kernel32.NewProc("CloseHandle").Call(mutex)
		}
		return
	}
	if mutex == 0 {
		message(0, "Pocket HTTP", "无法创建单实例锁："+mutexErr.Error(), 0x10)
		return
	}
	defer kernel32.NewProc("CloseHandle").Call(mutex)
	disk := &store{path: dataPath}
	workspace, err := disk.load()
	if err != nil {
		message(0, "工作区读取失败", err.Error()+"\n\n原文件未修改：\n"+dataPath, 0x10)
		return
	}
	if err = disk.backup(); err != nil {
		message(0, "备份失败", "未启动，避免覆盖旧数据："+err.Error(), 0x10)
		return
	}
	app = &application{w: workspace, disk: disk, controls: map[int]uintptr{}, labels: map[string]uintptr{}, treeIDs: map[uintptr]string{}, treeHandles: map[string]uintptr{}, jobs: make(chan saveJob, 1), saves: make(chan saveResult, 8), requests: make(chan requestResult, 1), pretty: true, mutex: mutex}
	instance, _, _ = kernel32.NewProc("GetModuleHandleW").Call(0)
	dpi := user32.NewProc("SetProcessDPIAware")
	dpi.Call()
	getDPI := user32.NewProc("GetDpiForSystem")
	if getDPI.Find() == nil {
		n, _, _ := getDPI.Call()
		if n >= 96 {
			scale = float64(n) / 96
		}
	}
	common := struct{ Size, Classes uint32 }{8, 0x0002 | 0x0008 | 0x4000}
	comctl32.NewProc("InitCommonControlsEx").Call(uintptr(unsafe.Pointer(&common)))
	makeFonts()
	registerClass("PocketHTTPNativeWindow", syscall.NewCallback(windowProc))
	registerClass("PocketHTTPNativeEditor", syscall.NewCallback(editorProc))
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(utf("PocketHTTPNativeWindow"))), uintptr(unsafe.Pointer(utf("Pocket HTTP 2.0 · Windows 原生版"))), 0x00cf0000, 100, 60, uintptr(px(1220)), uintptr(px(850)), 0, 0, instance, 0)
	if hwnd == 0 {
		message(0, "Pocket HTTP", "窗口创建失败："+err.Error(), 0x10)
		return
	}
	app.hwnd = hwnd
	go app.saveWorker()
	app.createControls()
	app.buildMenu()
	app.loadRequest(app.w.Draft)
	app.renderTree()
	app.renderHistory()
	app.setSaveStatus("已恢复本机工作区 · 无浏览器 / 无本地服务")
	app.layout()
	showWindow.Call(hwnd, 5)
	user32.NewProc("UpdateWindow").Call(hwnd)
	if nativeReady != nil {
		nativeReady <- hwnd
	}
	var m msg
	getMessage := user32.NewProc("GetMessageW")
	translate := user32.NewProc("TranslateMessage")
	dispatch := user32.NewProc("DispatchMessageW")
	dialogMessage := user32.NewProc("IsDialogMessageW")
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if app.editor != nil {
			if m.Message == wmKeyDown && m.Wparam == 0x1b {
				app.closeEditor()
				continue
			}
			ok, _, _ := dialogMessage.Call(app.editor.hwnd, uintptr(unsafe.Pointer(&m)))
			if ok != 0 {
				continue
			}
		} else {
			if m.Message == wmKeyDown && app.key(m.Wparam) {
				continue
			}
			ok, _, _ := dialogMessage.Call(hwnd, uintptr(unsafe.Pointer(&m)))
			if ok != 0 {
				continue
			}
		}
		translate.Call(uintptr(unsafe.Pointer(&m)))
		dispatch.Call(uintptr(unsafe.Pointer(&m)))
	}
	close(app.jobs)
	gdi32.NewProc("DeleteObject").Call(font)
	gdi32.NewProc("DeleteObject").Call(mono)
	if nativeClosed != nil {
		nativeClosed <- struct{}{}
	}
}
func registerClass(name string, callback uintptr) {
	cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
	icon, _, _ := user32.NewProc("LoadIconW").Call(0, 32512)
	class := wndClass{Style: 3, Callback: callback, Instance: instance, Cursor: cursor, Background: 16, ClassName: utf(name), Icon: icon}
	class.Size = uint32(unsafe.Sizeof(class))
	user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
}
func makeFonts() {
	makeFont := func(name string, size int) uintptr {
		h, _, _ := gdi32.NewProc("CreateFontW").Call(uintptr(int32(-px(size))), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf(name))))
		return h
	}
	font = makeFont("Segoe UI", 14)
	mono = makeFont("Consolas", 14)
}
func (a *application) add(id int, class, title string, style uint32) uintptr {
	h := control(a.hwnd, class, title, style, id)
	a.controls[id] = h
	return h
}
func (a *application) label(key, title string) uintptr {
	h := control(a.hwnd, "STATIC", title, 0, 0)
	a.labels[key] = h
	return h
}
func (a *application) createControls() {
	a.loading = true
	defer func() { a.loading = false }()
	button := func(id int, s string) uintptr { return a.add(id, "BUTTON", s, wsTabStop) }
	edit := func(id int, multi bool) uintptr {
		style := uint32(wsBorder | wsTabStop | esAutoHScroll)
		if multi {
			style = wsBorder | wsTabStop | wsVScroll | wsHScroll | esMultiline | esAutoVScroll | esAutoHScroll | esWantReturn
		}
		h := a.add(id, "EDIT", "", style)
		if multi {
			send(h, wmSetFont, mono, 1)
		}
		return h
	}
	combo := func(id int, items []string) uintptr {
		h := a.add(id, "COMBOBOX", "", wsTabStop|wsVScroll|3)
		addCombo(h, items)
		return h
	}
	a.label("collections", "COLLECTIONS")
	button(idNewCollection, "＋ 分组")
	button(idNewFolder, "＋ 文件夹")
	button(idEdit, "整理")
	button(idDelete, "删除")
	button(idImport, "导入")
	button(idExport, "导出")
	tree := a.add(idTree, "SysTreeView32", "", wsTabStop|wsBorder|0x27)
	theme := syscall.NewLazyDLL("uxtheme.dll").NewProc("SetWindowTheme")
	theme.Call(tree, uintptr(unsafe.Pointer(utf("Explorer"))), 0)
	a.label("history", "最近请求")
	button(idClearHistory, "清空")
	a.add(idHistory, "LISTBOX", "", wsTabStop|wsBorder|wsVScroll|1|0x100)
	button(idNew, "新建请求")
	a.name = a.label("name", "未命名请求")
	button(idSave, "保存到 Collection")
	button(idCurl, "复制 cURL")
	combo(idMethod, methods)
	edit(idURL, false)
	button(idSend, "发送")
	button(idCancel, "取消")
	enabled(a.controls[idCancel], false)
	tabs := a.add(idRequestTabs, "SysTabControl32", "", wsTabStop)
	for i, s := range []string{"Headers", "Authorization", "Body", "设置"} {
		addTab(tabs, s, i)
	}
	a.pages[0] = []uintptr{a.label("headersHint", "请求头（每行 Key: Value）；查询参数直接写在 URL 中"), edit(idHeaders, true)}
	authLabel := a.label("authType", "认证类型")
	authType := combo(idAuthType, []string{"No Auth", "Bearer Token", "Basic Auth", "API Key"})
	hint := a.label("authHint", "认证自动应用到发送 / cURL；启用后覆盖同名手动值。密钥明文保存在本机和导出文件中。")
	a.pages[1] = []uintptr{authLabel, authType, hint}
	group := func(index int, items ...uintptr) {
		a.authGroups[index] = items
		a.pages[1] = append(a.pages[1], items...)
	}
	group(0, a.label("authNone", "不自动添加认证信息。手动填写的认证请求头仍然生效。"))
	token := edit(idToken, false)
	username := edit(idUsername, false)
	password := edit(idPassword, false)
	key := edit(idKey, false)
	value := edit(idValue, false)
	in := combo(idAuthIn, []string{"Header", "Query Params"})
	group(1, a.label("token", "Token"), token)
	group(2, a.label("username", "Username"), username, a.label("password", "Password"), password)
	group(3, a.label("key", "Key"), key, a.label("value", "Value"), value, a.label("authIn", "添加到"), in)
	secret := a.add(idShowSecret, "BUTTON", "显示密钥", wsTabStop|3)
	a.pages[1] = append(a.pages[1], secret)
	a.secretControls = []uintptr{token, password, value}
	a.maskSecrets()
	bodyType := combo(idBodyType, []string{"Raw / 纯文本", "JSON", "Form URL Encoded"})
	bodyFormat := button(idFormat, "格式化 JSON")
	body := edit(idBody, true)
	a.pages[2] = []uintptr{bodyType, bodyFormat, body}
	a.pages[3] = []uintptr{a.label("timeout", "超时（秒，1–600）"), edit(idTimeout, false), a.add(idFollow, "BUTTON", "跟随重定向", wsTabStop|3), a.add(idInsecure, "BUTTON", "跳过 HTTPS 证书校验（仅可信开发接口）", wsTabStop|3), a.label("settingsHint", "默认验证证书。响应最多保留 2 MiB，界面预览 256 KiB；超限会明确提示。")}
	a.responseStatus = a.label("responseStatus", "Response · 等待发送")
	rTabs := a.add(idResponseTabs, "SysTabControl32", "", wsTabStop)
	addTab(rTabs, "Body", 0)
	addTab(rTabs, "Headers", 1)
	button(idPretty, "原文 / 格式化")
	button(idCopy, "复制")
	button(idDownload, "下载响应")
	out := edit(idOutput, true)
	send(out, 0x00cf, 1, 0)
	send(out, emSetLimit, 300<<10, 0)
	a.status = a.label("status", "")
}
func (a *application) buildMenu() {
	menu, _, _ := user32.NewProc("CreateMenu").Call()
	file, _, _ := user32.NewProc("CreatePopupMenu").Call()
	collection, _, _ := user32.NewProc("CreatePopupMenu").Call()
	request, _, _ := user32.NewProc("CreatePopupMenu").Call()
	appendMenu := user32.NewProc("AppendMenuW")
	add := func(m uintptr, id int, label string) {
		appendMenu.Call(m, 0, uintptr(id), uintptr(unsafe.Pointer(utf(label))))
	}
	add(file, idNew, "新建请求\tCtrl+N")
	add(file, idSave, "保存 / 整理请求\tCtrl+S")
	add(file, idImport, "导入 Collections\tCtrl+O")
	add(file, idExport, "导出 Collections")
	add(file, idQuit, "保存并退出")
	add(collection, idNewCollection, "新建 Collection")
	add(collection, idNewFolder, "新建子文件夹")
	add(collection, idEdit, "改名 / 移动")
	add(collection, idDelete, "删除所选节点")
	add(request, idSend, "发送\tCtrl+Enter")
	add(request, idCancel, "取消\tEsc")
	add(request, idCurl, "复制 cURL（Git Bash / WSL）")
	appendMenu.Call(menu, 0x10, file, uintptr(unsafe.Pointer(utf("文件(&F)"))))
	appendMenu.Call(menu, 0x10, collection, uintptr(unsafe.Pointer(utf("Collections(&C)"))))
	appendMenu.Call(menu, 0x10, request, uintptr(unsafe.Pointer(utf("请求(&R)"))))
	add(menu, idHelp, "使用说明(&H)")
	user32.NewProc("SetMenu").Call(a.hwnd, menu)
}
func (a *application) layout() {
	if a.controls[idURL] == 0 {
		return
	}
	var r rect
	getClientRect.Call(a.hwnd, uintptr(unsafe.Pointer(&r)))
	w, h := int(float64(r.Right)/scale), int(float64(r.Bottom)/scale)
	left := 260
	x := left + 18
	right := w - x - 16
	move(a.labels["collections"], 12, 13, 120, 22)
	move(a.controls[idNewCollection], 12, 39, 112, 28)
	move(a.controls[idNewFolder], 130, 39, 118, 28)
	move(a.controls[idEdit], 12, 74, 52, 26)
	move(a.controls[idDelete], 70, 74, 52, 26)
	move(a.controls[idImport], 128, 74, 56, 26)
	move(a.controls[idExport], 190, 74, 58, 26)
	treeHeight := max(160, h-345)
	move(a.controls[idTree], 12, 108, 236, treeHeight)
	hy := 116 + treeHeight
	move(a.labels["history"], 12, hy, 140, 22)
	move(a.controls[idClearHistory], 195, hy-3, 53, 25)
	move(a.controls[idHistory], 12, hy+28, 236, max(70, h-hy-70))
	move(a.controls[idNew], x, 12, 96, 28)
	move(a.name, x+106, 17, max(100, right-390), 24)
	move(a.controls[idSave], x+right-268, 12, 154, 28)
	move(a.controls[idCurl], x+right-104, 12, 104, 28)
	move(a.controls[idMethod], x, 52, 96, 240)
	move(a.controls[idURL], x+104, 52, right-278, 30)
	move(a.controls[idSend], x+right-166, 52, 80, 30)
	move(a.controls[idCancel], x+right-78, 52, 78, 30)
	top := 96
	pageH := min(280, max(200, (h-290)/2))
	move(a.controls[idRequestTabs], x, top, right, pageH+36)
	px0, py0, pw := x+12, top+39, right-24
	move(a.labels["headersHint"], px0, py0, pw, 20)
	move(a.controls[idHeaders], px0, py0+26, pw, pageH-42)
	move(a.labels["authType"], px0, py0+2, 110, 22)
	move(a.controls[idAuthType], px0, py0+29, 160, 180)
	ax := px0 + 184
	aw := pw - 184
	move(a.labels["authNone"], ax, py0+32, aw, 45)
	move(a.labels["token"], ax, py0, aw, 22)
	move(a.controls[idToken], ax, py0+25, aw, 28)
	move(a.labels["username"], ax, py0, aw, 22)
	move(a.controls[idUsername], ax, py0+25, aw, 28)
	move(a.labels["password"], ax, py0+62, aw, 22)
	move(a.controls[idPassword], ax, py0+87, aw, 28)
	move(a.labels["key"], ax, py0, aw, 22)
	move(a.controls[idKey], ax, py0+25, max(120, aw/2-8), 28)
	move(a.labels["authIn"], ax+aw/2+8, py0, max(100, aw/2-8), 22)
	move(a.controls[idAuthIn], ax+aw/2+8, py0+25, max(100, aw/2-8), 160)
	move(a.labels["value"], ax, py0+62, aw, 22)
	move(a.controls[idValue], ax, py0+87, aw, 28)
	move(a.controls[idShowSecret], ax, py0+123, 160, 24)
	move(a.labels["authHint"], px0, py0+pageH-43, pw, 40)
	move(a.controls[idBodyType], px0, py0, 205, 160)
	move(a.controls[idFormat], px0+216, py0, 124, 28)
	move(a.controls[idBody], px0, py0+36, pw, pageH-52)
	move(a.labels["timeout"], px0, py0+4, 185, 22)
	move(a.controls[idTimeout], px0+190, py0, 90, 28)
	move(a.controls[idFollow], px0, py0+45, 230, 26)
	move(a.controls[idInsecure], px0, py0+83, pw, 26)
	move(a.labels["settingsHint"], px0, py0+132, pw, 50)
	ry := top + pageH + 48
	move(a.responseStatus, x, ry, right, 38)
	ry += 42
	move(a.controls[idResponseTabs], x, ry, max(180, right-350), 30)
	move(a.controls[idPretty], x+right-340, ry, 134, 28)
	move(a.controls[idCopy], x+right-198, ry, 70, 28)
	move(a.controls[idDownload], x+right-120, ry, 120, 28)
	move(a.controls[idOutput], x, ry+37, right, max(90, h-ry-80))
	move(a.status, 12, h-30, w-24, 24)
	a.showPage()
}
func (a *application) showPage() {
	selected := int(send(a.controls[idRequestTabs], tcGetCurSel, 0, 0))
	for i, items := range a.pages {
		for _, h := range items {
			visible(h, i == selected)
		}
	}
	if selected == 1 {
		which := comboIndex(a.controls[idAuthType])
		for i, items := range a.authGroups {
			for _, h := range items {
				visible(h, i == which)
			}
		}
		visible(a.controls[idShowSecret], which != 0)
	}
}
func (a *application) maskSecrets() {
	char := uintptr('●')
	if checked(a.controls[idShowSecret]) {
		char = 0
	}
	for _, h := range a.secretControls {
		send(h, 0x00cc, char, 0)
		user32.NewProc("InvalidateRect").Call(h, 0, 1)
	}
}
func (a *application) loadRequest(r Request) {
	a.loading = true
	defer func() { a.loading = false }()
	normalizeRequest(&r)
	methodIndex := 0
	for i, m := range methods {
		if r.Method == m {
			methodIndex = i
		}
	}
	setCombo(a.controls[idMethod], methodIndex)
	setText(a.controls[idURL], r.URL)
	setText(a.controls[idHeaders], crlf(r.Headers))
	setText(a.controls[idBody], crlf(r.Body))
	bodyIndex := 0
	if r.BodyType == "json" {
		bodyIndex = 1
	} else if r.BodyType == "form" {
		bodyIndex = 2
	}
	setCombo(a.controls[idBodyType], bodyIndex)
	setText(a.controls[idTimeout], strconv.Itoa(r.Timeout))
	setChecked(a.controls[idFollow], r.Follow)
	setChecked(a.controls[idInsecure], r.Insecure)
	types := []string{"none", "bearer", "basic", "apikey"}
	for i, t := range types {
		if r.Auth.Type == t {
			setCombo(a.controls[idAuthType], i)
		}
	}
	for id, value := range map[int]string{idToken: r.Auth.Token, idUsername: r.Auth.Username, idPassword: r.Auth.Password, idKey: r.Auth.Key, idValue: r.Auth.Value} {
		setText(a.controls[id], value)
	}
	setCombo(a.controls[idAuthIn], 0)
	if r.Auth.In == "query" {
		setCombo(a.controls[idAuthIn], 1)
	}
	setChecked(a.controls[idShowSecret], false)
	a.maskSecrets()
	a.showPage()
	a.updateName()
}
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}
func lf(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
func choice(h uintptr, values []string) string {
	i := comboIndex(h)
	if i < 0 || i >= len(values) {
		return values[0]
	}
	return values[i]
}
func (a *application) snapshot() Request {
	timeout, _ := strconv.Atoi(text(a.controls[idTimeout]))
	return Request{Method: choice(a.controls[idMethod], methods), URL: strings.TrimSpace(text(a.controls[idURL])), Headers: lf(text(a.controls[idHeaders])), Body: lf(text(a.controls[idBody])), BodyType: choice(a.controls[idBodyType], []string{"raw", "json", "form"}), Timeout: timeout, Follow: checked(a.controls[idFollow]), Insecure: checked(a.controls[idInsecure]), Auth: Auth{Type: choice(a.controls[idAuthType], []string{"none", "bearer", "basic", "apikey"}), Token: text(a.controls[idToken]), Username: text(a.controls[idUsername]), Password: text(a.controls[idPassword]), Key: text(a.controls[idKey]), Value: text(a.controls[idValue]), In: choice(a.controls[idAuthIn], []string{"header", "query"})}}
}
func (a *application) capture() {
	a.w.Draft = a.snapshot()
	if node, _ := a.w.find(a.w.SelectedID); node != nil && node.Type == "request" {
		r := a.w.Draft
		node.Request = &r
	}
	a.w.Opened = a.w.Opened[:0]
	for handle, id := range a.treeIDs {
		item := tvItem{Mask: 8, Item: handle, StateMask: 0x20}
		send(a.controls[idTree], tvGetItem, 0, uintptr(unsafe.Pointer(&item)))
		if item.State&0x20 != 0 {
			a.w.Opened = append(a.w.Opened, id)
		}
	}
}
func (a *application) updateName() {
	node, parent := a.w.find(a.w.SelectedID)
	name := "未命名请求 · 自动保存草稿"
	if node != nil {
		name = node.Name
		if parent != nil {
			name = parent.Name + " / " + name
		}
	}
	setText(a.name, name)
	if node == nil {
		setText(a.controls[idSave], "保存到 Collection")
	} else {
		setText(a.controls[idSave], "整理请求")
	}
}
func (a *application) markDirty() {
	if a.loading || a.closing {
		return
	}
	a.seq++
	a.setSaveStatus("有修改 · 即将自动保存")
	setTimer.Call(a.hwnd, 1, 350, 0)
}
func (a *application) setSaveStatus(s string) { setText(a.status, s) }
func (a *application) save(quit bool) bool {
	killTimer.Call(a.hwnd, 1)
	a.capture()
	data, err := workspaceBytes(a.w)
	if err != nil {
		a.setSaveStatus("未保存：" + err.Error())
		if quit {
			message(a.hwnd, "无法退出前保存", err.Error(), 0x10)
		}
		return false
	}
	job := saveJob{data, a.seq, quit}
	select {
	case <-a.jobs:
	default:
	}
	a.jobs <- job
	a.setSaveStatus("正在写入本机工作区…")
	if quit {
		a.closing = true
		enabled(a.hwnd, false)
	}
	return true
}
func (a *application) saveWorker() {
	for job := range a.jobs {
		err := a.disk.write(job.data)
		a.saves <- saveResult{job.seq, job.quit, err}
		postMsg.Call(a.hwnd, wmSaveDone, 0, 0)
	}
}
func (a *application) handleSaves() {
	for {
		select {
		case result := <-a.saves:
			if result.err != nil {
				a.setSaveStatus("保存失败：" + result.err.Error())
				if result.quit {
					a.closing = false
					enabled(a.hwnd, true)
					message(a.hwnd, "保存失败，未退出", result.err.Error()+"\n可导出 Collections 备份后重试。", 0x10)
				}
			} else if result.quit {
				destroyWindow.Call(a.hwnd)
				return
			} else if result.seq == a.seq {
				a.setSaveStatus("已自动保存到本机 · Ctrl+Enter 发送 · Ctrl+S 保存")
			}
		default:
			return
		}
	}
}
func (a *application) renderTree() {
	a.buildingTree = true
	defer func() { a.buildingTree = false }()
	send(a.controls[idTree], tvDelete, 0, ^uintptr(65535))
	a.treeIDs = map[uintptr]string{}
	a.treeHandles = map[string]uintptr{}
	expanded := map[string]bool{}
	for _, id := range a.w.Opened {
		expanded[id] = true
	}
	var insert func([]*Node, uintptr)
	insert = func(nodes []*Node, parent uintptr) {
		for _, n := range nodes {
			label := n.Name
			if n.Type == "request" {
				label = n.Request.Method + "  " + label
			} else if n.Type == "collection" {
				label = "▣  " + label
			} else {
				label = "▱  " + label
			}
			p := utf(label)
			item := tvInsertStruct{Parent: parent, After: ^uintptr(65533), Item: tvItem{Mask: 1 | 0x40, Text: p, Children: int32(len(n.Children))}}
			handle := send(a.controls[idTree], tvInsert, 0, uintptr(unsafe.Pointer(&item)))
			runtime.KeepAlive(p)
			a.treeIDs[handle] = n.ID
			a.treeHandles[n.ID] = handle
			insert(n.Children, handle)
			if expanded[n.ID] {
				send(a.controls[idTree], tvExpand, 2, handle)
			}
		}
	}
	insert(a.w.Collections, ^uintptr(65535))
	selected := a.treeSelection
	if node, _ := a.w.find(selected); node == nil {
		selected = a.w.SelectedID
	}
	if handle := a.treeHandles[selected]; handle != 0 {
		send(a.controls[idTree], tvSelect, 9, handle)
	}
	a.updateName()
}
func (a *application) renderHistory() {
	a.loading = true
	defer func() { a.loading = false }()
	send(a.controls[idHistory], lbResetContent, 0, 0)
	for _, r := range a.w.History {
		value := r.Method + "  " + r.URL
		p := utf(value)
		send(a.controls[idHistory], lbAddString, 0, uintptr(unsafe.Pointer(p)))
		runtime.KeepAlive(p)
	}
}
func (a *application) selectedNode() *Node {
	handle := send(a.controls[idTree], tvGetNext, 9, 0)
	n, _ := a.w.find(a.treeIDs[handle])
	return n
}
func (a *application) startRequest() {
	if a.cancel != nil {
		return
	}
	r := a.snapshot()
	if r.Timeout < 1 || r.Timeout > 600 {
		message(a.hwnd, "检查设置", "超时必须为 1–600 秒。", 0x30)
		return
	}
	if _, err := prepareRequest(context.Background(), r); err != nil {
		message(a.hwnd, "请求无效", err.Error(), 0x30)
		return
	}
	a.capture()
	a.w.addHistory(r)
	a.renderHistory()
	a.markDirty()
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.last = nil
	setText(a.controls[idOutput], "")
	setText(a.responseStatus, "正在请求…")
	enabled(a.controls[idSend], false)
	enabled(a.controls[idCancel], true)
	go func() {
		response, err := perform(ctx, r)
		a.requests <- requestResult{response, err}
		postMsg.Call(a.hwnd, wmRequestDone, 0, 0)
	}()
}
func (a *application) finishRequest() {
	select {
	case result := <-a.requests:
		if a.cancel != nil {
			a.cancel()
			a.cancel = nil
		}
		enabled(a.controls[idSend], true)
		enabled(a.controls[idCancel], false)
		if result.err != nil {
			messageText := result.err.Error()
			if strings.Contains(messageText, "context canceled") {
				messageText = "请求已取消"
			}
			setText(a.responseStatus, "请求未完成")
			setText(a.controls[idOutput], messageText)
			return
		}
		a.last = result.response
		a.pretty = true
		summary := fmt.Sprintf("%s  ·  %d ms  ·  %.2f KiB", a.last.Status, a.last.Elapsed.Milliseconds(), float64(len(a.last.Data))/1024)
		if a.last.Truncated {
			summary += "  ·  已截断至 2 MiB（下载也只包含保留部分）"
		}
		setText(a.responseStatus, summary)
		a.renderResponse()
	default:
	}
}
func (a *application) responseText(full bool) string {
	if a.last == nil {
		return ""
	}
	if send(a.controls[idResponseTabs], tcGetCurSel, 0, 0) == 1 {
		return headerText(a.last.Headers)
	}
	data := a.last.Data
	if !utf8.Valid(data) {
		return "[二进制响应，点击「下载响应」保存文件]"
	}
	if full {
		return string(data)
	}
	if a.pretty && len(data) <= 256<<10 {
		var b bytes.Buffer
		if json.Indent(&b, data, "", "  ") == nil {
			data = b.Bytes()
		}
	}
	if len(data) > 256<<10 {
		cut := 256 << 10
		for cut > 0 && !utf8.RuneStart(data[cut]) {
			cut--
		}
		return string(data[:cut]) + "\r\n\r\n[仅预览前 256 KiB；复制 / 下载可取得本次保留的完整响应]"
	}
	return string(data)
}
func (a *application) renderResponse() { setText(a.controls[idOutput], crlf(a.responseText(false))) }
func (a *application) key(vk uintptr) bool {
	keyState := user32.NewProc("GetKeyState")
	ctrl, _, _ := keyState.Call(0x11)
	if int16(ctrl) < 0 {
		switch vk {
		case 0x0d:
			a.startRequest()
			return true
		case 'S':
			a.command(idSave)
			return true
		case 'N':
			a.command(idNew)
			return true
		case 'L':
			setFocus.Call(a.controls[idURL])
			return true
		case 'O':
			a.command(idImport)
			return true
		case 'A':
			focus, _, _ := getFocus.Call()
			class := make([]uint16, 32)
			user32.NewProc("GetClassNameW").Call(focus, uintptr(unsafe.Pointer(&class[0])), 32)
			if syscall.UTF16ToString(class) == "Edit" {
				send(focus, emSetSel, 0, ^uintptr(0))
				return true
			}
		}
	}
	if vk == 0x1b && a.cancel != nil {
		a.cancel()
		return true
	}
	return false
}
func (a *application) command(id int) {
	if a.closing {
		return
	}
	switch id {
	case idNew:
		a.capture()
		a.w.SelectedID = ""
		a.treeSelection = ""
		a.w.Draft = defaultRequest()
		a.loadRequest(a.w.Draft)
		a.renderTree()
		a.markDirty()
		setFocus.Call(a.controls[idURL])
	case idNewCollection:
		a.openEditor("collection", "")
	case idNewFolder:
		n := a.selectedNode()
		if n == nil {
			message(a.hwnd, "新建文件夹", "请先选择一个 Collection 或文件夹。", 0x40)
			return
		}
		if n.Type == "request" {
			_, n = a.w.find(n.ID)
		}
		if n != nil {
			a.openEditor("folder", n.ID)
		}
	case idEdit:
		n := a.selectedNode()
		if n != nil {
			a.openEditor("edit", n.ID)
		}
	case idSave:
		if a.w.SelectedID != "" {
			a.openEditor("edit", a.w.SelectedID)
		} else {
			a.openEditor("save", "")
		}
	case idDelete:
		a.deleteSelected()
	case idSend:
		a.startRequest()
	case idCancel:
		if a.cancel != nil {
			a.cancel()
		}
	case idFormat:
		var b bytes.Buffer
		if err := json.Indent(&b, []byte(lf(text(a.controls[idBody]))), "", "  "); err != nil {
			message(a.hwnd, "JSON 格式错误", err.Error(), 0x30)
		} else {
			a.loading = true
			setText(a.controls[idBody], crlf(b.String()))
			setCombo(a.controls[idBodyType], 1)
			a.loading = false
			a.markDirty()
		}
	case idAuthType:
		a.showPage()
		a.markDirty()
	case idShowSecret:
		a.maskSecrets()
	case idFollow, idInsecure:
		a.markDirty()
	case idCurl:
		value, err := curlText(a.snapshot())
		if err != nil {
			message(a.hwnd, "无法生成 cURL", err.Error(), 0x30)
		} else if err = clipboard(a.hwnd, value); err != nil {
			message(a.hwnd, "复制失败", err.Error(), 0x30)
		} else {
			a.setSaveStatus("cURL 已复制（POSIX shell 格式，适用于 Git Bash / WSL）")
		}
	case idPretty:
		a.pretty = !a.pretty
		a.renderResponse()
	case idCopy:
		if a.last != nil {
			value := a.responseText(false)
			if len(a.last.Data) > 256<<10 && send(a.controls[idResponseTabs], tcGetCurSel, 0, 0) == 0 {
				value = a.responseText(true)
			}
			if err := clipboard(a.hwnd, value); err != nil {
				message(a.hwnd, "复制失败", err.Error(), 0x30)
			}
		}
	case idDownload:
		if a.last != nil {
			name := "response.bin"
			ct := a.last.Headers.Get("Content-Type")
			if strings.Contains(ct, "json") {
				name = "response.json"
			} else if strings.HasPrefix(ct, "text/") {
				name = "response.txt"
			}
			if path, ok := fileDialog(a.hwnd, true, "保存响应（最大 2 MiB）", name, ""); ok {
				if err := os.WriteFile(path, a.last.Data, 0600); err != nil {
					message(a.hwnd, "保存响应失败", err.Error(), 0x10)
				}
			}
		}
	case idClearHistory:
		if message(a.hwnd, "清空历史", "清空最近请求？Collections 会保留。", 0x24) == 6 {
			a.w.History = []Request{}
			a.renderHistory()
			a.markDirty()
		}
	case idImport:
		a.importFile()
	case idExport:
		data, err := exportCollections(a.w.Collections)
		if err != nil {
			message(a.hwnd, "导出失败", err.Error(), 0x10)
			return
		}
		if path, ok := fileDialog(a.hwnd, true, "导出 Collections（含认证密钥）", "pocket-http-collections.json", "json"); ok {
			if err = os.WriteFile(path, data, 0600); err != nil {
				message(a.hwnd, "导出失败", err.Error(), 0x10)
			}
		}
	case idHelp:
		message(a.hwnd, "Pocket HTTP 2.0 · 原生版", "Go + Win32 原生控件，不依赖浏览器 / WebView / 本地 HTTP 服务。\n\nCollections：新建分组、子文件夹，整理可改名和移动请求。\nAuthorization：No Auth / Bearer / Basic / API Key（Header 或 Query）。\nCtrl+Enter 发送，Esc 取消，Ctrl+N 新建，Ctrl+L 地址，Ctrl+S 整理保存。\n\n编辑后约 350 ms 自动保存；关闭窗口会等待写入成功。\n数据："+a.disk.path+"\n旧工作区首次使用前备份为 .before-native.bak。\n\n响应最多保留 2 MiB，界面预览 256 KiB；历史最多 30 条 / 512 KiB。\n认证密钥在工作区与导出文件中明文保存，请妥善保管。\n支持本应用旧版收藏和 v1.1 Collections，不支持 Postman 原生集合格式。", 0x40)
	case idQuit:
		send(a.hwnd, wmClose, 0, 0)
	}
}
func (a *application) importFile() {
	path, ok := fileDialog(a.hwnd, false, "导入本应用 Collections / 旧版收藏", "", "json")
	if !ok {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		message(a.hwnd, "导入失败", err.Error(), 0x10)
		return
	}
	data, err := readBounded(f, maxWorkspace)
	f.Close()
	if err != nil {
		message(a.hwnd, "导入失败", err.Error(), 0x30)
		return
	}
	nodes, err := importCollections(data)
	if err != nil {
		message(a.hwnd, "导入失败", err.Error(), 0x30)
		return
	}
	a.capture()
	before, _ := json.Marshal(a.w)
	a.w.Collections = append(a.w.Collections, nodes...)
	if err = validateWorkspace(&a.w); err == nil {
		_, err = workspaceBytes(a.w)
	}
	if err != nil {
		json.Unmarshal(before, &a.w)
		message(a.hwnd, "导入失败", err.Error(), 0x30)
		return
	}
	for _, n := range nodes {
		a.w.Opened = append(a.w.Opened, n.ID)
	}
	a.renderTree()
	a.markDirty()
}
func (a *application) deleteSelected() {
	n := a.selectedNode()
	if n == nil {
		return
	}
	suffix := ""
	if n.Type != "request" {
		suffix = "及其中所有请求"
	}
	if message(a.hwnd, "删除", "删除「"+n.Name+"」"+suffix+"？", 0x24) != 6 {
		return
	}
	a.capture()
	_, parent := a.w.find(n.ID)
	items := &a.w.Collections
	if parent != nil {
		items = &parent.Children
	}
	for i, item := range *items {
		if item.ID == n.ID {
			*items = append((*items)[:i], (*items)[i+1:]...)
			break
		}
	}
	if selected, _ := a.w.find(a.w.SelectedID); selected == nil {
		a.w.SelectedID = ""
	}
	a.treeSelection = ""
	a.renderTree()
	a.markDirty()
}
func windowProc(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	if app == nil {
		r, _, _ := defWindow.Call(hwnd, uintptr(msg), w, l)
		return r
	}
	switch msg {
	case wmCreate:
		app.hwnd = hwnd
		return 0
	case wmSize:
		app.layout()
		return 0
	case wmGetMinMax:
		m := (*minMaxInfo)(unsafe.Pointer(l))
		m.MinTrack = point{int32(px(1050)), int32(px(760))}
		return 0
	case wmTimer:
		if w == 1 {
			app.save(false)
		}
		return 0
	case wmCommand:
		id := int(w & 0xffff)
		notification := int(w >> 16)
		if app.loading {
			return 0
		}
		if id == idHistory && notification == 1 {
			index := int(int32(send(app.controls[idHistory], lbGetCurSel, 0, 0)))
			if index >= 0 && index < len(app.w.History) {
				app.capture()
				r := app.w.History[index]
				app.w.SelectedID = ""
				app.treeSelection = ""
				app.w.Draft = r
				app.loadRequest(r)
				app.renderTree()
				app.markDirty()
			}
			return 0
		}
		if notification == 0 {
			app.command(id)
		} else if notification == 0x0300 && id >= idURL && id <= idTimeout {
			app.markDirty()
		} else if notification == 1 && (id == idMethod || id == idAuthType || id == idBodyType || id == idAuthIn) {
			if id == idAuthType {
				app.showPage()
			}
			app.markDirty()
		}
		return 0
	case wmNotify:
		n := (*nmhdr)(unsafe.Pointer(l))
		if n.Hwnd == app.controls[idRequestTabs] && n.Code == -551 {
			app.showPage()
		}
		if n.Hwnd == app.controls[idResponseTabs] && n.Code == -551 {
			app.renderResponse()
		}
		if n.Hwnd == app.controls[idTree] && !app.buildingTree {
			if n.Code == -451 {
				handle := send(n.Hwnd, tvGetNext, 9, 0)
				id := app.treeIDs[handle]
				app.treeSelection = id
				node, _ := app.w.find(id)
				if node != nil && node.Type == "request" && app.w.SelectedID != id {
					app.capture()
					app.w.SelectedID = id
					app.w.Draft = *node.Request
					app.loadRequest(*node.Request)
					app.markDirty()
				}
			} else if n.Code == -455 {
				app.markDirty()
			}
		}
		return 0
	case wmRequestDone:
		app.finishRequest()
		return 0
	case wmSaveDone:
		app.handleSaves()
		return 0
	case wmClose:
		if app.closing {
			return 0
		}
		if app.editor != nil {
			app.closeEditor()
		}
		if app.cancel != nil {
			app.cancel()
		}
		app.save(true)
		return 0
	case wmDestroy:
		killTimer.Call(hwnd, 1)
		user32.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	r, _, _ := defWindow.Call(hwnd, uintptr(msg), w, l)
	return r
}
