package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestV11WorkspaceCompatibility(t *testing.T) {
	data := `{"version":2,"collections":[{"id":"c","type":"collection","name":"Journal","children":[{"id":"f","type":"folder","name":"Users","children":[{"id":"r","type":"request","name":"Add user","request":{"method":"POST","url":"http://localhost/users","headers":"X-From: v1.1","body":"{}","bodytype":"json","timeout":30,"follow":true,"insecure":false,"auth":{"type":"bearer","token":"old-secret","in":"header"}}}]}]}],"history":[],"draft":{"method":"GET","url":"http://localhost/draft","headers":"","body":"unsent","bodytype":"raw","timeout":15,"follow":true,"auth":{"type":"none"}},"selectedId":null,"opened":["c","f"],"updatedAt":123}`
	path := filepath.Join(t.TempDir(), "workspace.json")
	os.WriteFile(path, []byte(data), 0600)
	s := &store{path: path}
	w, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	n, _ := w.find("r")
	if n.Request.Auth.Token != "old-secret" || w.Draft.Body != "unsent" || len(w.Opened) != 2 {
		t.Fatal("v1.1 data not retained")
	}
	if err = s.backup(); err != nil {
		t.Fatal(err)
	}
	w.Draft.URL = "http://localhost/new"
	b, _ := workspaceBytes(w)
	if err = s.write(b); err != nil {
		t.Fatal(err)
	}
	backup, _ := os.ReadFile(path + ".before-native.bak")
	if string(backup) != data {
		t.Fatal("backup altered original")
	}
	if err = s.backup(); err != nil {
		t.Fatal(err)
	}
	again, err := s.load()
	if err != nil || again.Draft.URL != "http://localhost/new" {
		t.Fatal("restart persistence failed", err)
	}
	empty := defaultWorkspace()
	empty.Collections = []*Node{{ID: "empty", Type: "collection", Name: "Empty"}}
	b, _ = workspaceBytes(empty)
	if !bytes.Contains(b, []byte(`"children":[]`)) {
		t.Fatal("empty collection incompatible with v1.1")
	}
}
func TestAuthAndWireRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.NewEncoder(w).Encode(map[string]any{"auth": r.Header.Get("Authorization"), "keys": r.Header.Values("X-Key"), "query": r.URL.Query().Get("key"), "body": string(body), "type": r.Header.Get("Content-Type"), "method": r.Method})
	}))
	defer server.Close()
	for _, tc := range []struct {
		name             string
		a                Auth
		auth, key, query string
	}{{"bearer", Auth{Type: "bearer", Token: "secret"}, "Bearer secret", "old", "old"}, {"basic", Auth{Type: "basic", Username: "测试", Password: "p:a"}, "Basic " + base64.StdEncoding.EncodeToString([]byte("测试:p:a")), "old", "old"}, {"api-header", Auth{Type: "apikey", Key: "X-Key", Value: "new"}, "Manual", "new", "old"}, {"api-query", Auth{Type: "apikey", Key: "key", Value: "a&中", In: "query"}, "Manual", "old", "a&中"}, {"none", Auth{Type: "none"}, "Manual", "old", "old"}} {
		t.Run(tc.name, func(t *testing.T) {
			r := defaultRequest()
			r.Method = "PATCH"
			r.URL = server.URL + "?key=old"
			r.Body = `{"hello":1}`
			r.BodyType = "json"
			r.Headers = "Authorization: Manual\nX-Key: old"
			r.Auth = tc.a
			res, err := perform(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Auth                      string
				Keys                      []string
				Query, Body, Type, Method string
			}
			json.Unmarshal(res.Data, &got)
			if got.Auth != tc.auth || len(got.Keys) != 1 || got.Keys[0] != tc.key || got.Query != tc.query || got.Method != "PATCH" || got.Type != "application/json" || got.Body != r.Body {
				t.Fatalf("%+v", got)
			}
			curl, err := curlText(r)
			if err != nil || !strings.Contains(curl, "curl -X 'PATCH'") {
				t.Fatal(curl, err)
			}
		})
	}
}
func TestRedirectBinaryCapAndTLS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/binary", 302)
			return
		}
		w.Write(bytes.Repeat([]byte{255}, maxResponse+20))
	}))
	defer server.Close()
	r := defaultRequest()
	r.URL = server.URL + "/redirect"
	r.Follow = false
	res, err := perform(context.Background(), r)
	if err != nil || res.Code != 302 {
		t.Fatal(res, err)
	}
	r.Follow = true
	res, err = perform(context.Background(), r)
	if err != nil || len(res.Data) != maxResponse || !res.Truncated || res.Data[0] != 255 {
		t.Fatal("binary cap failed", err)
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer tls.Close()
	r.URL = tls.URL
	if _, err = perform(context.Background(), r); err == nil {
		t.Fatal("untrusted TLS accepted")
	}
	r.Insecure = true
	res, err = perform(context.Background(), r)
	if err != nil || string(res.Data) != "ok" {
		t.Fatal(err)
	}
}
func TestCancelAndURLValidation(t *testing.T) {
	entered := make(chan bool, 1)
	stopped := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- true; <-r.Context().Done(); stopped <- true }))
	defer server.Close()
	r := defaultRequest()
	r.URL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := perform(ctx, r); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel missing")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel stalled")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream still running")
	}
	for _, u := range []string{"file:///tmp/x", "ftp://test", "http://user:pass@localhost"} {
		r.URL = u
		if _, err := prepareRequest(context.Background(), r); err == nil {
			t.Fatal(u)
		}
	}
}
func TestImportExportAndCapacity(t *testing.T) {
	nodes, err := importCollections([]byte(`[{"method":"GET","url":"http://localhost","name":"Legacy","auth":{"type":"basic","username":"u","password":"p"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := exportCollections(nodes)
	round, err := importCollections(data)
	if err != nil || round[0].Children[0].Request.Auth.Password != "p" || round[0].Children[0].Name != "Legacy" || !round[0].Children[0].Request.Follow || round[0].ID == nodes[0].ID {
		t.Fatal("import/export failed", err)
	}
	bad := []byte(`{"format":"pocket-http-collections","version":2,"collections":[{"id":"x","type":"collection","name":"X","children":[{"id":"x","type":"folder","name":"Y"}]}]}`)
	if _, err = importCollections(bad); err == nil {
		t.Fatal("duplicate ids accepted")
	}
	w := defaultWorkspace()
	w.Draft.Body = strings.Repeat("x", maxWorkspace)
	if _, err = workspaceBytes(w); err == nil {
		t.Fatal("workspace cap ignored")
	}
	r := defaultRequest()
	r.Body = strings.Repeat("x", 65<<10)
	w.addHistory(r)
	if len(w.History) != 0 {
		t.Fatal("large history retained")
	}
	r.Body = "small"
	for i := 0; i < 40; i++ {
		r.Body += "x"
		w.addHistory(r)
	}
	if len(w.History) != 30 {
		t.Fatal("history count cap ignored")
	}
	path := filepath.Join(t.TempDir(), "new", "workspace.json")
	s := &store{path: path}
	if err = s.write([]byte(`{"version":2,"collections":[]}`)); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err = s.write(bytes.Repeat([]byte{'x'}, maxWorkspace+1)); err == nil {
		t.Fatal("oversize file accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("last valid save overwritten")
	}
	temps, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp"))
	if len(temps) != 0 {
		t.Fatal("temp file leaked")
	}
}

func TestConsecutiveHistoryDeduplication(t *testing.T) {
	w := defaultWorkspace()
	a := defaultRequest()
	w.addHistory(a)
	w.addHistory(a)
	if len(w.History) != 1 {
		t.Fatal("consecutive duplicate retained")
	}
	b := a
	b.Auth.Token = "different"
	w.addHistory(b)
	w.addHistory(a)
	if len(w.History) != 3 {
		t.Fatal("distinct or nonconsecutive request removed")
	}
}
