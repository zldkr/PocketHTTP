package main

import (
	"encoding/json"
	"strings"
	"unsafe"
)

type nodeEditor struct {
	hwnd, name, parent uintptr
	mode, target       string
	destIDs            []string
}

func (a *application) openEditor(mode, target string) {
	if a.editor != nil {
		return
	}
	a.capture()
	title := "保存请求"
	initial := "新请求"
	switch mode {
	case "collection":
		title = "新建 Collection"
		initial = "新 Collection"
	case "folder":
		title = "新建文件夹"
		initial = "新文件夹"
	case "edit":
		title = "改名 / 移动"
		n, _ := a.w.find(target)
		if n == nil {
			return
		}
		initial = n.Name
	case "save":
		if a.w.Draft.URL != "" {
			initial = a.w.Draft.URL
		}
	}
	editor := &nodeEditor{mode: mode, target: target}
	a.editor = editor
	hwnd, _, _ := createWindow.Call(1, uintptr(unsafe.Pointer(utf("PocketHTTPNativeEditor"))), uintptr(unsafe.Pointer(utf(title))), 0x80c80000, 180, 180, uintptr(px(580)), uintptr(px(270)), a.hwnd, 0, instance, 0)
	if hwnd == 0 {
		a.editor = nil
		return
	}
	editor.hwnd = hwnd
	nameLabel := control(hwnd, "STATIC", "名称", 0, 0)
	move(nameLabel, 18, 18, 520, 22)
	editor.name = control(hwnd, "EDIT", initial, wsTabStop|wsBorder|esAutoHScroll, idDialogName)
	send(editor.name, emSetLimit, 120, 0)
	move(editor.name, 18, 44, 525, 30)
	parentLabel := control(hwnd, "STATIC", "保存位置", 0, 0)
	move(parentLabel, 18, 88, 525, 22)
	editor.parent = control(hwnd, "COMBOBOX", "", wsTabStop|wsVScroll|3, idDialogParent)
	move(editor.parent, 18, 114, 525, 240)
	exclude := ""
	if mode == "edit" {
		exclude = target
	}
	var labels []string
	var walk func([]*Node, string)
	walk = func(items []*Node, prefix string) {
		for _, n := range items {
			if n.Type == "request" || n.ID == exclude {
				continue
			}
			name := n.Name
			if prefix != "" {
				name = prefix + " / " + name
			}
			labels = append(labels, name)
			editor.destIDs = append(editor.destIDs, n.ID)
			walk(n.Children, name)
		}
	}
	walk(a.w.Collections, "")
	if mode == "save" && len(labels) == 0 {
		labels = []string{"我的 Collection（新建）"}
		editor.destIDs = []string{""}
	}
	addCombo(editor.parent, labels)
	preferred := target
	if mode == "edit" {
		_, parent := a.w.find(target)
		if parent != nil {
			preferred = parent.ID
		}
	} else if mode == "save" {
		n := a.selectedNode()
		if n != nil {
			preferred = n.ID
			if n.Type == "request" {
				_, p := a.w.find(n.ID)
				if p != nil {
					preferred = p.ID
				}
			}
		}
	}
	for i, id := range editor.destIDs {
		if id == preferred {
			setCombo(editor.parent, i)
		}
	}
	root := mode == "collection"
	if n, _ := a.w.find(target); mode == "edit" && n != nil && n.Type == "collection" {
		root = true
	}
	visible(parentLabel, !root)
	visible(editor.parent, !root)
	cancel := control(hwnd, "BUTTON", "取消", wsTabStop, idDialogCancel)
	move(cancel, 345, 170, 92, 30)
	ok := control(hwnd, "BUTTON", "保存", wsTabStop|1, idDialogOK)
	move(ok, 450, 170, 92, 30)
	enabled(a.hwnd, false)
	showWindow.Call(hwnd, 5)
	setFocus.Call(editor.name)
	send(editor.name, emSetSel, 0, ^uintptr(0))
}
func (a *application) closeEditor() {
	if a.editor == nil {
		return
	}
	h := a.editor.hwnd
	a.editor = nil
	destroyWindow.Call(h)
	enabled(a.hwnd, true)
	user32.NewProc("SetForegroundWindow").Call(a.hwnd)
	setFocus.Call(a.controls[idURL])
}
func (a *application) commitEditor() {
	e := a.editor
	if e == nil {
		return
	}
	name := strings.TrimSpace(text(e.name))
	if name == "" {
		message(e.hwnd, "名称无效", "请填写名称。", 0x30)
		return
	}
	a.capture()
	before, _ := json.Marshal(a.w)
	oldSelection := a.treeSelection
	index := comboIndex(e.parent)
	destID := ""
	if index >= 0 && index < len(e.destIDs) {
		destID = e.destIDs[index]
	}
	var dest *Node
	if destID != "" {
		dest, _ = a.w.find(destID)
	}
	fail := func(s string) {
		json.Unmarshal(before, &a.w)
		a.treeSelection = oldSelection
		message(e.hwnd, "无法保存", s, 0x30)
	}
	if e.mode == "collection" {
		n := &Node{ID: newID(), Type: "collection", Name: name, Children: []*Node{}}
		a.w.Collections = append(a.w.Collections, n)
		a.w.Opened = append(a.w.Opened, n.ID)
		a.treeSelection = n.ID
	} else if e.mode == "edit" {
		n, parent := a.w.find(e.target)
		if n == nil {
			fail("节点不存在")
			return
		}
		n.Name = name
		if n.Type != "collection" {
			if dest == nil || dest.Type == "request" {
				fail("请选择保存位置")
				return
			}
			if parent != dest {
				items := &parent.Children
				for i, v := range *items {
					if v.ID == n.ID {
						*items = append((*items)[:i], (*items)[i+1:]...)
						break
					}
				}
				dest.Children = append(dest.Children, n)
				a.w.Opened = append(a.w.Opened, dest.ID)
			}
		}
		a.treeSelection = n.ID
	} else {
		if dest == nil && e.mode == "save" && len(a.w.Collections) == 0 {
			dest = &Node{ID: newID(), Type: "collection", Name: "我的 Collection", Children: []*Node{}}
			a.w.Collections = append(a.w.Collections, dest)
		}
		if dest == nil || dest.Type == "request" {
			fail("请选择 Collection / 文件夹")
			return
		}
		n := &Node{ID: newID(), Name: name}
		if e.mode == "folder" {
			n.Type = "folder"
			n.Children = []*Node{}
		} else {
			n.Type = "request"
			r := a.snapshot()
			n.Request = &r
			a.w.SelectedID = n.ID
		}
		dest.Children = append(dest.Children, n)
		a.w.Opened = append(a.w.Opened, dest.ID)
		a.treeSelection = n.ID
	}
	if err := validateWorkspace(&a.w); err != nil {
		fail(err.Error())
		return
	}
	if _, err := workspaceBytes(a.w); err != nil {
		fail(err.Error())
		return
	}
	a.closeEditor()
	a.renderTree()
	a.markDirty()
}
func editorProc(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	if app != nil {
		switch msg {
		case wmCommand:
			switch int(w & 0xffff) {
			case idDialogOK:
				app.commitEditor()
				return 0
			case idDialogCancel:
				app.closeEditor()
				return 0
			}
		case wmClose:
			app.closeEditor()
			return 0
		}
	}
	r, _, _ := defWindow.Call(hwnd, uintptr(msg), w, l)
	return r
}
