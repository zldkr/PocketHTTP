package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const maxResponse = 2 << 20
const maxWorkspace = 2 << 20

var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

type Auth struct {
	Type     string `json:"type"`
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
	Key      string `json:"key"`
	Value    string `json:"value"`
	In       string `json:"in"`
}
type Request struct {
	Method   string `json:"method"`
	URL      string `json:"url"`
	Headers  string `json:"headers"`
	Body     string `json:"body"`
	BodyType string `json:"bodytype"`
	Timeout  int    `json:"timeout"`
	Follow   bool   `json:"follow"`
	Insecure bool   `json:"insecure"`
	Auth     Auth   `json:"auth"`
}

// Missing legacy fields use the same defaults as the previous browser UI.
func (r *Request) UnmarshalJSON(data []byte) error {
	type fields Request
	v := fields(defaultRequest())
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*r = Request(v)
	return nil
}

type Node struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Children []*Node  `json:"children,omitempty"`
	Request  *Request `json:"request,omitempty"`
}

// Containers must serialize empty children as [], for compatibility with v1.1.
func (n Node) MarshalJSON() ([]byte, error) {
	type alias Node
	if n.Type == "request" {
		return json.Marshal(alias(n))
	}
	return json.Marshal(struct {
		ID       string  `json:"id"`
		Type     string  `json:"type"`
		Name     string  `json:"name"`
		Children []*Node `json:"children"`
	}{n.ID, n.Type, n.Name, nonNil(n.Children)})
}
func nonNil(v []*Node) []*Node {
	if v == nil {
		return []*Node{}
	}
	return v
}

type Workspace struct {
	Version     int       `json:"version"`
	Collections []*Node   `json:"collections"`
	History     []Request `json:"history"`
	Draft       Request   `json:"draft"`
	SelectedID  string    `json:"selectedId"`
	Opened      []string  `json:"opened"`
	UpdatedAt   int64     `json:"updatedAt"`
}

func defaultRequest() Request {
	return Request{Method: "GET", BodyType: "raw", Timeout: 30, Follow: true, Auth: Auth{Type: "none", In: "header"}}
}
func defaultWorkspace() Workspace {
	return Workspace{Version: 2, Collections: []*Node{}, History: []Request{}, Draft: defaultRequest(), Opened: []string{}}
}
func newID() string { var b [16]byte; rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func normalizeRequest(r *Request) error {
	if r.Method == "" {
		r.Method = "GET"
	}
	found := false
	for _, m := range methods {
		if r.Method == m {
			found = true
		}
	}
	if !found {
		return errors.New("请求方法无效")
	}
	if r.BodyType != "json" && r.BodyType != "form" {
		r.BodyType = "raw"
	}
	if r.Timeout < 1 || r.Timeout > 600 {
		r.Timeout = 30
	}
	if r.Auth.Type == "" {
		r.Auth.Type = "none"
	}
	switch r.Auth.Type {
	case "none", "bearer", "basic", "apikey":
	default:
		return errors.New("不支持的认证类型")
	}
	if r.Auth.In != "query" {
		r.Auth.In = "header"
	}
	return nil
}
func validateCollections(nodes []*Node) error {
	seen := map[string]bool{}
	total, requests := 0, 0
	var walk func([]*Node, int, bool) error
	walk = func(items []*Node, depth int, root bool) error {
		if depth > 8 && len(items) > 0 {
			return errors.New("文件夹最多嵌套 8 层")
		}
		for _, n := range items {
			if n == nil || n.ID == "" || seen[n.ID] || strings.TrimSpace(n.Name) == "" || len([]rune(n.Name)) > 120 {
				return errors.New("Collection 名称或 ID 无效")
			}
			seen[n.ID] = true
			total++
			if total > 300 {
				return errors.New("分组、文件夹和请求合计最多 300 个")
			}
			if root && n.Type != "collection" || !root && n.Type == "collection" {
				return errors.New("Collection 层级无效")
			}
			switch n.Type {
			case "request":
				requests++
				if requests > 100 {
					return errors.New("最多保存 100 条请求")
				}
				if n.Request == nil {
					return errors.New("缺少请求内容")
				}
				if err := normalizeRequest(n.Request); err != nil {
					return err
				}
				n.Children = nil
			case "folder", "collection":
				if err := walk(n.Children, depth+1, false); err != nil {
					return err
				}
				n.Children = nonNil(n.Children)
				n.Request = nil
			default:
				return errors.New("节点类型无效")
			}
		}
		return nil
	}
	return walk(nodes, 0, true)
}
func validateWorkspace(w *Workspace) error {
	if w.Version != 2 {
		return errors.New("不支持的工作区版本")
	}
	w.Collections = nonNil(w.Collections)
	if err := validateCollections(w.Collections); err != nil {
		return err
	}
	if err := normalizeRequest(&w.Draft); err != nil {
		return err
	}
	if node, _ := w.find(w.SelectedID); node == nil || node.Type != "request" {
		w.SelectedID = ""
	}
	if len(w.History) > 30 {
		w.History = w.History[:30]
	}
	for i := range w.History {
		if err := normalizeRequest(&w.History[i]); err != nil {
			return err
		}
	}
	if len(w.Opened) > 300 {
		w.Opened = w.Opened[:300]
	}
	return nil
}
func (w *Workspace) find(id string) (node, parent *Node) {
	var search func([]*Node, *Node) (*Node, *Node)
	search = func(items []*Node, p *Node) (*Node, *Node) {
		for _, n := range items {
			if n.ID == id {
				return n, p
			}
			if v, parent := search(n.Children, n); v != nil {
				return v, parent
			}
		}
		return nil, nil
	}
	return search(w.Collections, nil)
}
func (w *Workspace) addHistory(r Request) {
	b, _ := json.Marshal(r)
	if len(b) > 64<<10 {
		return
	}
	if len(w.History) == 0 || w.History[0] != r {
		w.History = append([]Request{r}, w.History...)
	}
	if node, _ := w.find(w.SelectedID); node == nil || node.Type != "request" {
		w.SelectedID = ""
	}
	if len(w.History) > 30 {
		w.History = w.History[:30]
	}
	for len(w.History) > 0 {
		data, _ := json.Marshal(w.History)
		if len(data) <= 512<<10 {
			break
		}
		w.History = w.History[:len(w.History)-1]
	}
}
func workspacePath() (string, error) {
	if p := os.Getenv("POCKET_DATA_DIR"); p != "" {
		return filepath.Join(p, "workspace.json"), nil
	}
	p, err := os.UserConfigDir()
	return filepath.Join(p, "PocketHTTP", "workspace.json"), err
}

type store struct {
	path string
	mu   sync.Mutex
}

func (s *store) load() (Workspace, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultWorkspace(), nil
	}
	if err != nil {
		return Workspace{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxWorkspace+1))
	if err != nil || len(b) > maxWorkspace {
		return Workspace{}, errors.New("工作区超过 2 MiB 或读取失败")
	}
	w := Workspace{Draft: defaultRequest()}
	if err = json.Unmarshal(b, &w); err != nil {
		return w, fmt.Errorf("工作区 JSON 无效：%w", err)
	}
	return w, validateWorkspace(&w)
}
func workspaceBytes(w Workspace) ([]byte, error) {
	w.UpdatedAt = time.Now().UnixMilli()
	b, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	if len(b) > maxWorkspace {
		return nil, errors.New("工作区超过 2 MiB，尚未保存；请缩小正文或导出并删除部分请求")
	}
	return b, nil
}
func (s *store) write(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(b) > maxWorkspace {
		return errors.New("工作区超过 2 MiB")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), "workspace-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Windows readers and antivirus scanners can briefly hold a file without
	// FILE_SHARE_DELETE. Retry replacement, preserving the previous valid save.
	for attempt := 0; attempt < 8; attempt++ {
		err = os.Rename(name, s.path)
		if err == nil || runtime.GOOS != "windows" {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 15 * time.Millisecond)
	}
	return err
}
func (s *store) backup() error {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	dest, err := os.OpenFile(s.path+".before-native.bak", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = io.Copy(dest, io.LimitReader(f, maxWorkspace+1))
	closeErr := dest.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func prepareRequest(ctx context.Context, in Request) (*http.Request, error) {
	u, err := url.Parse(in.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("请输入完整的 http:// 或 https:// URL；认证请在 Authorization 中填写")
	}
	if len(in.Body) > maxResponse {
		return nil, errors.New("请求体不能超过 2 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, strings.NewReader(in.Body))
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.ReplaceAll(in.Headers, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" {
			return nil, errors.New("请求头应为 Key: Value，每行一个")
		}
		if strings.EqualFold(key, "Host") {
			req.Host = value
		} else {
			req.Header.Add(key, value)
		}
	}
	if req.Header.Get("Content-Type") == "" {
		switch in.BodyType {
		case "json":
			req.Header.Set("Content-Type", "application/json")
		case "form":
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	a := in.Auth
	switch a.Type {
	case "bearer":
		if strings.TrimSpace(a.Token) == "" {
			return nil, errors.New("请填写 Bearer Token")
		}
		req.Header.Set("Authorization", "Bearer "+a.Token)
	case "basic":
		if strings.Contains(a.Username, ":") {
			return nil, errors.New("Basic Auth 用户名不能包含冒号")
		}
		req.SetBasicAuth(a.Username, a.Password)
	case "apikey":
		if strings.TrimSpace(a.Key) == "" {
			return nil, errors.New("请填写 API Key 名称")
		}
		if a.In == "query" {
			q := req.URL.Query()
			q.Set(a.Key, a.Value)
			req.URL.RawQuery = q.Encode()
		} else {
			req.Header.Set(a.Key, a.Value)
		}
	}
	return req, nil
}

type Response struct {
	Status    string
	Code      int
	Headers   http.Header
	Data      []byte
	Elapsed   time.Duration
	URL       string
	Truncated bool
}

func perform(ctx context.Context, in Request) (*Response, error) {
	timeout := in.Timeout
	if timeout < 1 || timeout > 600 {
		timeout = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	req, err := prepareRequest(ctx, in)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxResponseHeaderBytes = 64 << 10
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: in.Insecure}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	if !in.Follow {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	start := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	out := &Response{Status: res.Status, Code: res.StatusCode, Headers: res.Header, Data: data, Elapsed: time.Since(start), URL: res.Request.URL.String()}
	if len(data) > maxResponse {
		out.Truncated = true
		out.Data = data[:maxResponse]
	}
	return out, nil
}
func headerText(h http.Header) string { var b bytes.Buffer; h.Write(&b); return b.String() }
func curlText(in Request) (string, error) {
	req, err := prepareRequest(context.Background(), in)
	if err != nil {
		return "", err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	args := []string{"curl", "-X", quote(in.Method), "--max-time", fmt.Sprint(in.Timeout)}
	if in.Follow {
		args = append(args, "-L")
	}
	if in.Insecure {
		args = append(args, "-k")
	}
	for k, values := range req.Header {
		for _, v := range values {
			args = append(args, "-H", quote(k+": "+v))
		}
	}
	if req.Host != req.URL.Host {
		args = append(args, "-H", quote("Host: "+req.Host))
	}
	if in.Body != "" {
		args = append(args, "--data-raw", quote(in.Body))
	}
	args = append(args, quote(req.URL.String()))
	return strings.Join(args, " "), nil
}
func importCollections(b []byte) ([]*Node, error) {
	if len(b) > maxWorkspace {
		return nil, errors.New("导入文件不能超过 2 MiB")
	}
	var nodes []*Node
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("空文件")
	}
	if bytes.TrimSpace(b)[0] == '[' {
		var legacy []json.RawMessage
		if err := json.Unmarshal(b, &legacy); err != nil {
			return nil, err
		}
		group := &Node{ID: newID(), Type: "collection", Name: "旧版收藏", Children: []*Node{}}
		for _, raw := range legacy {
			var r Request
			var meta struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, err
			}
			json.Unmarshal(raw, &meta)
			name := meta.Name
			if name == "" {
				name = r.URL
			}
			if len([]rune(name)) > 120 {
				name = string([]rune(name)[:120])
			}
			group.Children = append(group.Children, &Node{ID: newID(), Type: "request", Name: name, Request: &r})
		}
		nodes = []*Node{group}
	} else {
		var data struct {
			Format      string  `json:"format"`
			Version     int     `json:"version"`
			Collections []*Node `json:"collections"`
		}
		if err := json.Unmarshal(b, &data); err != nil {
			return nil, err
		}
		if data.Format != "pocket-http-collections" || data.Version != 2 || data.Collections == nil {
			return nil, errors.New("不支持的 Collections 格式；支持本应用 v1.1 导出及旧版收藏数组")
		}
		nodes = data.Collections
	}
	if err := validateCollections(nodes); err != nil {
		return nil, err
	}
	var rekey func([]*Node)
	rekey = func(items []*Node) {
		for _, n := range items {
			n.ID = newID()
			rekey(n.Children)
		}
	}
	rekey(nodes)
	return nodes, nil
}
func exportCollections(nodes []*Node) ([]byte, error) {
	return json.MarshalIndent(struct {
		Format      string  `json:"format"`
		Version     int     `json:"version"`
		Collections []*Node `json:"collections"`
	}{"pocket-http-collections", 2, nonNil(nodes)}, "", "  ")
}

func readBounded(r io.Reader, limit int) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(limit+1)))
	if err == nil && len(b) > limit {
		err = errors.New("文件超过大小上限")
	}
	return b, err
}
