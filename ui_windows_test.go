package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestNativeWindow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("POCKET_DATA_DIR", dir)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/big" {
			io.WriteString(w, strings.Repeat("x", maxResponse+100))
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"auth": r.Header.Get("Authorization"), "key": r.Header.Get("X-Key"), "query": r.URL.Query().Get("key"), "body": string(body), "method": r.Method})
	}))
	defer server.Close()
	original := defaultWorkspace()
	r := defaultRequest()
	r.URL = server.URL
	r.Method = "POST"
	r.BodyType = "json"
	r.Body = `{"name":"Fred"}`
	r.Auth = Auth{Type: "bearer", Token: "old-secret"}
	original.Collections = []*Node{{ID: "c", Name: "Journal", Type: "collection", Children: []*Node{{ID: "f", Name: "Users", Type: "folder", Children: []*Node{{ID: "r", Name: "Original", Type: "request", Request: &r}}}}}}
	original.Draft = r
	original.SelectedID = "r"
	original.Opened = []string{"c", "f"}
	data, _ := workspaceBytes(original)
	path := filepath.Join(dir, "workspace.json")
	os.WriteFile(path, data, 0600)
	nativeReady = make(chan uintptr)
	defer func() { nativeReady = nil }()
	nativeClosed = nil
	done := make(chan struct{})
	go func() { runApp(); close(done) }()
	select {
	case <-nativeReady:
	case <-time.After(20 * time.Second):
		t.Fatal("native window not created")
	}
	wait := func(label string, fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if fn() {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("timeout: " + label + " status=" + text(app.status))
	}
	command := func(id int) { send(app.hwnd, wmCommand, uintptr(id), 0) }
	choose := func(id, index int) {
		setCombo(app.controls[id], index)
		send(app.hwnd, wmCommand, uintptr(id|1<<16), app.controls[id])
	}
	tab := func(index int) {
		send(app.controls[idRequestTabs], tcSetCurSel, uintptr(index), 0)
		n := nmhdr{Hwnd: app.controls[idRequestTabs], ID: idRequestTabs, Code: -551}
		send(app.hwnd, wmNotify, idRequestTabs, uintptr(unsafe.Pointer(&n)))
	}
	read := func() Workspace { var w Workspace; b, _ := os.ReadFile(path); json.Unmarshal(b, &w); return w }
	if text(app.controls[idToken]) != "old-secret" || text(app.controls[idBody]) != r.Body {
		t.Fatal("v1.1 form not restored")
	}
	tab(1)
	setText(app.controls[idToken], "native-secret")
	setText(app.controls[idHeaders], "Authorization: Manual")
	command(idSend)
	wait("bearer response", func() bool { return strings.Contains(text(app.responseStatus), "200 OK") })
	if !strings.Contains(text(app.controls[idOutput]), "Bearer native-secret") {
		t.Fatal("native bearer not applied")
	}
	choose(idAuthType, 2)
	setText(app.controls[idUsername], "测试")
	setText(app.controls[idPassword], "p:a")
	command(idSend)
	wait("basic response", func() bool { return strings.Contains(text(app.responseStatus), "200 OK") })
	if !strings.Contains(text(app.controls[idOutput]), "Basic 5rWL6K+VOnA6YQ==") {
		t.Fatal(text(app.controls[idOutput]))
	}
	choose(idAuthType, 3)
	setText(app.controls[idKey], "X-Key")
	setText(app.controls[idValue], "native-key")
	choose(idAuthIn, 0)
	command(idSend)
	wait("API key response", func() bool { return strings.Contains(text(app.responseStatus), "200 OK") })
	if !strings.Contains(text(app.controls[idOutput]), "native-key") {
		t.Fatal("native API key failed")
	}
	choose(idAuthType, 0)
	setText(app.controls[idURL], server.URL+"/slow")
	command(idSend)
	command(idCancel)
	wait("cancel", func() bool { return strings.Contains(text(app.controls[idOutput]), "请求已取消") })
	setText(app.controls[idURL], server.URL+"/big")
	command(idSend)
	wait("large response", func() bool { return strings.Contains(text(app.responseStatus), "已截断") })
	if !strings.Contains(text(app.controls[idOutput]), "仅预览前 256 KiB") {
		t.Fatal("native preview cap missing")
	}
	command(idNewCollection)
	setText(app.editor.name, "Extra")
	send(app.editor.hwnd, wmCommand, idDialogOK, 0)
	wait("new collection", func() bool { return len(read().Collections) == 2 })
	command(idNewFolder)
	setText(app.editor.name, "Internal")
	send(app.editor.hwnd, wmCommand, idDialogOK, 0)
	wait("new folder", func() bool { w := read(); return len(w.Collections) == 2 && len(w.Collections[1].Children) == 1 })
	command(idNew)
	setText(app.controls[idURL], server.URL+"/created")
	choose(idMethod, 1)
	choose(idAuthType, 2)
	setText(app.controls[idUsername], "u")
	setText(app.controls[idPassword], "p")
	command(idSave)
	setText(app.editor.name, "Created")
	setCombo(app.editor.parent, 3)
	send(app.editor.hwnd, wmCommand, idDialogOK, 0)
	wait("saved request", func() bool {
		w := read()
		return len(w.Collections) == 2 && len(w.Collections[1].Children) == 1 && len(w.Collections[1].Children[0].Children) == 1
	})
	command(idSave)
	setText(app.editor.name, "Renamed")
	setCombo(app.editor.parent, 2)
	send(app.editor.hwnd, wmCommand, idDialogOK, 0)
	wait("move and rename", func() bool {
		w := read()
		return len(w.Collections) == 2 && len(w.Collections[1].Children) == 2 && w.Collections[1].Children[1].Name == "Renamed"
	})

	// Exercise the actual Windows common save/open dialogs and v1.1 import.
	commonDialog := func(id int, title, filename string) {
		finished := make(chan struct{})
		go func() { command(id); close(finished) }()
		var dialog uintptr
		wait("common file dialog", func() bool {
			dialog, _, _ = user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(utf("#32770"))), uintptr(unsafe.Pointer(utf(title))))
			return dialog != 0
		})
		filenameControl, _, _ := user32.NewProc("GetDlgItem").Call(dialog, 1148)
		if filenameControl == 0 {
			filenameControl, _, _ = user32.NewProc("GetDlgItem").Call(dialog, 1152)
		}
		if filenameControl == 0 {
			t.Fatal("file dialog filename control missing")
		}
		setText(filenameControl, filename)
		send(dialog, wmCommand, 1, 0)
		select {
		case <-finished:
		case <-time.After(8 * time.Second):
			t.Fatal("common dialog did not complete")
		}
	}
	exported := filepath.Join(dir, "export.json")
	commonDialog(idExport, "导出 Collections（含认证密钥）", exported)
	exportedBytes, err := os.ReadFile(exported)
	if err != nil || !strings.Contains(string(exportedBytes), "pocket-http-collections") {
		t.Fatal("native export dialog failed", err)
	}
	importPath := filepath.Join(dir, "legacy.json")
	os.WriteFile(importPath, []byte(`[{"method":"GET","url":"http://localhost/imported","name":"Imported"}]`), 0600)
	commonDialog(idImport, "导入本应用 Collections / 旧版收藏", importPath)
	wait("native import dialog", func() bool { return len(read().Collections) == 3 })
	// Delete an empty folder through the native confirmation dialog.
	savedForDelete := read()
	folderID := savedForDelete.Collections[1].Children[0].ID
	handle := app.treeHandles[folderID]
	send(app.controls[idTree], tvSelect, 9, handle)
	deleted := make(chan struct{})
	go func() { command(idDelete); close(deleted) }()
	var confirm uintptr
	wait("delete confirmation", func() bool {
		confirm, _, _ = user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(utf("#32770"))), uintptr(unsafe.Pointer(utf("删除"))))
		return confirm != 0
	})
	send(confirm, wmCommand, 6, 0)
	select {
	case <-deleted:
	case <-time.After(8 * time.Second):
		t.Fatal("delete did not complete")
	}
	wait("folder delete", func() bool {
		w := read()
		return len(w.Collections) == 3 && len(w.Collections[1].Children) == 1 && w.Collections[1].Children[0].Name == "Renamed"
	})

	// Closing immediately after an edit must flush it, without relying on a debounce timer.
	longBody := strings.Repeat("z", 90<<10)
	setText(app.controls[idBody], longBody)
	send(app.hwnd, wmClose, 0, 0)
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("close did not finish save")
	}
	saved := read()
	if saved.Draft.Body != longBody || saved.SelectedID == "" || saved.Draft.Auth.Username != "u" {
		t.Fatal("close did not preserve latest edits")
	}
	backup, _ := os.ReadFile(path + ".before-native.bak")
	if string(backup) != string(data) {
		t.Fatal("v1.1 backup was replaced")
	}
	// Recreate the native window and restore the last selected request from disk.
	done = make(chan struct{})
	go func() { runApp(); close(done) }()
	select {
	case <-nativeReady:
	case <-time.After(15 * time.Second):
		t.Fatal("restart failed")
	}
	if text(app.controls[idBody]) != longBody || text(app.controls[idUsername]) != "u" || !strings.Contains(text(app.name), "Renamed") {
		t.Fatal("native restart did not restore form")
	}
	send(app.hwnd, wmClose, 0, 0)
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("restart close failed")
	}
	t.Log("Win32 UI: legacy restore, requests/auth, cancel/caps, Collection/folder CRUD, request move/rename, close flush and restart passed")
}
