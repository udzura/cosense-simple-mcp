package cosense

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const oldCommit = "111111111111111111111111"
const newCommit = "222222222222222222222222"

func fixturePage(exists bool) Page {
	p := Page{ID: "aaaaaaaaaaaaaaaaaaaaaaaa", Title: "Test", Persistent: &exists, Lines: []Line{{ID: "aaaaaaaaaaaaaaaaaaaaaaaa", Text: "Test"}}, Links: []string{"link"}, ProjectLinks: []string{}}
	if exists {
		p.CommitID = oldCommit
		p.Lines = append(p.Lines, Line{ID: "bbbbbbbbbbbbbbbbbbbbbbbb", Text: "original [link]"})
	}
	return p
}

type mockCosense struct {
	mu              sync.Mutex
	page            Page
	reads           int
	commits         []commit
	ackError        string
	loseAck         bool
	corruptReadback bool
	changeOnRecheck bool
	createOnRecheck bool
}

func (m *mockCosense) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/socket.io/" {
			m.socket(t, w, r)
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		switch r.URL.Path {
		case "/api/users/me":
			w.Write([]byte(`{"id":"cccccccccccccccccccccccc"}`))
		case "/api/projects/test-project":
			w.Write([]byte(`{"id":"dddddddddddddddddddddddd"}`))
		case "/api/pages/test-project/Test":
			m.reads++
			if m.reads == 2 && m.changeOnRecheck {
				m.page.CommitID = newCommit
			}
			if m.reads == 2 && m.createOnRecheck {
				v := true
				m.page.Persistent = &v
				m.page.CommitID = newCommit
			}
			if m.corruptReadback && len(m.commits) > 0 {
				m.page.Lines[0].Text = "changed"
			}
			json.NewEncoder(w).Encode(m.page)
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}
}

func (m *mockCosense) socket(t *testing.T, w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("EIO") != "4" || r.URL.Query().Get("transport") != "websocket" || !strings.Contains(r.Header.Get("Cookie"), "connect.sid=secret-cookie") {
		t.Error("bad websocket handshake")
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		t.Error(err)
		return
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ws.Write(ctx, websocket.MessageText, []byte(`0{"sid":"mock","pingInterval":25000,"pingTimeout":20000}`))
	_, b, err := ws.Read(ctx)
	if err != nil || string(b) != "40" {
		t.Errorf("connect %q %v", b, err)
		return
	}
	ws.Write(ctx, websocket.MessageText, []byte(`40{"sid":"mock"}`))
	// Check Engine.IO heartbeat handling before the commit acknowledgement.
	ws.Write(ctx, websocket.MessageText, []byte("2"))
	var packet []byte
	for {
		_, b, err = ws.Read(ctx)
		if err != nil {
			return
		}
		if string(b) == "3" {
			continue
		}
		packet = b
		break
	}
	if !strings.HasPrefix(string(packet), "421[") {
		t.Errorf("event %q", packet)
		return
	}
	var event []json.RawMessage
	if json.Unmarshal(packet[3:], &event) != nil || len(event) != 2 {
		t.Error("invalid event")
		return
	}
	var name string
	json.Unmarshal(event[0], &name)
	var req struct {
		Method string `json:"method"`
		Data   commit `json:"data"`
	}
	if err := json.Unmarshal(event[1], &req); err != nil {
		t.Error(err)
		return
	}
	if name != "socket.io-request" || req.Method != "commit" || !req.Data.Freeze || req.Data.Kind != "page" {
		t.Error("bad commit envelope")
	}
	m.mu.Lock()
	m.commits = append(m.commits, req.Data)
	if m.ackError != "" {
		payload, _ := json.Marshal([]any{map[string]any{"error": map[string]string{"name": m.ackError, "message": "sensitive body secret-cookie"}}})
		m.mu.Unlock()
		ws.Write(ctx, websocket.MessageText, append([]byte("431"), payload...))
		return
	}
	if *m.page.Persistent && (req.Data.ParentID == nil || *req.Data.ParentID != m.page.CommitID) {
		t.Error("missing revision guard")
	}
	for _, change := range req.Data.Changes {
		for _, key := range []string{"links", "projectLinks", "descriptions"} {
			if value, ok := change[key]; ok && value == nil {
				t.Errorf("null %s metadata", key)
			}
		}
		if _, ok := change["_update"]; ok {
			t.Error("existing line replacement")
		}
		if _, ok := change["_delete"]; ok {
			t.Error("existing line deletion")
		}
		if v, ok := change["_insert"]; ok {
			if v != "_end" {
				t.Error("nonappend insertion")
			}
			b, _ := json.Marshal(change["lines"])
			var l Line
			json.Unmarshal(b, &l)
			if !lineID.MatchString(l.ID) {
				t.Error("bad line id")
			}
			m.page.Lines = append(m.page.Lines, l)
		}
	}
	v := true
	m.page.Persistent = &v
	m.page.CommitID = newCommit
	lose := m.loseAck
	m.mu.Unlock()
	if lose {
		return
	}
	ws.Write(ctx, websocket.MessageText, []byte(`431[{"data":{"commitId":"`+newCommit+`"}}]`))
	// Let the client close after reading the ack.
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			return
		}
	}
}

func TestCreateAndAppend(t *testing.T) {
	for _, create := range []bool{true, false} {
		t.Run(map[bool]string{true: "create", false: "append"}[create], func(t *testing.T) {
			m := &mockCosense{page: fixturePage(!create)}
			c := testClient(t, m.handler(t))
			var out WriteResult
			var err error
			if create {
				out, err = c.CreatePage(context.Background(), "Test", "新しい [知識]\ncode:go\n fmt.Println(1)")
			} else {
				out, err = c.AppendPage(context.Background(), "Test", "新しい [知識]\ncode:go\n fmt.Println(1)", oldCommit)
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.CommitID != newCommit || out.URL != "https://scrapbox.io/test-project/Test" || len(out.Warnings) == 0 {
				t.Fatalf("%+v", out)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.commits) != 1 {
				t.Fatal("commit count")
			}
			if create && m.commits[0].ParentID != nil {
				t.Error("new page parent should be null")
			}
			if !create && (m.page.Lines[1].Text != "original [link]" || m.page.Lines[2].Text != "") {
				t.Error("old contents changed or separator missing")
			}
		})
	}
}

func TestWriteProtection(t *testing.T) {
	cases := []struct {
		name     string
		create   bool
		page     Page
		expected string
		want     error
	}{
		{"existing", true, fixturePage(true), "", ErrExists},
		{"missing", false, fixturePage(false), oldCommit, ErrNotFound},
		{"stale revision", false, fixturePage(true), newCommit, ErrConflict},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := &mockCosense{page: tt.page}
			c := testClient(t, m.handler(t))
			var err error
			if tt.create {
				_, err = c.CreatePage(context.Background(), "Test", "body")
			} else {
				_, err = c.AppendPage(context.Background(), "Test", "body", tt.expected)
			}
			if !errors.Is(err, tt.want) {
				t.Fatal(err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.commits) != 0 {
				t.Fatal("unexpected write")
			}
		})
	}
}

func TestConflictAndUncertainty(t *testing.T) {
	cases := []struct {
		name string
		m    mockCosense
		want error
	}{
		{"server conflict", mockCosense{ackError: "NotFastForwardError"}, ErrConflict},
		{"duplicate title", mockCosense{ackError: "DuplicateTitleError"}, ErrExists},
		{"lost ack", mockCosense{loseAck: true}, ErrUncertain},
		{"readback mismatch", mockCosense{corruptReadback: true}, ErrUncertain},
		{"changed before send", mockCosense{changeOnRecheck: true}, ErrConflict},
	}
	for i := range cases {
		tt := &cases[i]
		t.Run(tt.name, func(t *testing.T) {
			m := &tt.m
			m.page = fixturePage(true)
			c := testClient(t, m.handler(t))
			_, err := c.AppendPage(context.Background(), "Test", "body", oldCommit)
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v; got %v", tt.want, err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.commits) > 1 {
				t.Fatal("automatic retry")
			}
			if m.changeOnRecheck && len(m.commits) != 0 {
				t.Fatal("wrote after concurrent change")
			}
		})
	}
}

func TestRetriesCannotDuplicate(t *testing.T) {
	m := &mockCosense{page: fixturePage(true), loseAck: true}
	c := testClient(t, m.handler(t))
	_, err := c.AppendPage(context.Background(), "Test", "body", oldCommit)
	if !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	_, err = c.AppendPage(context.Background(), "Test", "body", oldCommit)
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.commits) != 1 {
		t.Fatal("duplicated write")
	}
}

func TestCreationRace(t *testing.T) {
	m := &mockCosense{page: fixturePage(false), createOnRecheck: true}
	c := testClient(t, m.handler(t))
	if _, err := c.CreatePage(context.Background(), "Test", "body"); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.commits) != 0 {
		t.Fatal("overwrote page")
	}
}

func Test404DoesNotCreate(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	if _, err := c.CreatePage(context.Background(), "Test", "body"); err == nil {
		t.Fatal("invented new page metadata")
	}
}

func TestCollectLinks(t *testing.T) {
	links, projects := collectLinks("[* [新しい]] [link] [/project/page]\n`[ignore]`\ncode:go\n [also-ignore]", []string{"link"}, nil)
	if strings.Join(links, ",") != "link,新しい" || strings.Join(projects, ",") != "/project/page" {
		t.Fatalf("%v %v", links, projects)
	}
	links, projects = collectLinks("plain body", []string{}, []string{})
	if links == nil || projects == nil {
		t.Fatal("empty arrays must not become null metadata")
	}
}

func TestConcurrentAppends(t *testing.T) {
	m := &mockCosense{page: fixturePage(true)}
	c := testClient(t, m.handler(t))
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := c.AppendPage(context.Background(), "Test", "body", oldCommit); results <- err }()
	}
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, ErrConflict)) || (second == nil && errors.Is(first, ErrConflict))) {
		t.Fatalf("%v; %v", first, second)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.commits) != 1 {
		t.Fatal("duplicate concurrent commit")
	}
}
