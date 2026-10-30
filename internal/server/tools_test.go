package server

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/udzura/cosense-simple-mcp/internal/cosense"
)

func TestToolDiscoveryAndValidation(t *testing.T) {
	ctx := context.Background()
	c, err := cosense.New("test-project", "")
	if err != nil {
		t.Fatal(err)
	}
	s := New(c)
	a, b := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 6 {
		t.Fatalf("got %d tools", len(list.Tools))
	}
	for _, tool := range list.Tools {
		if tool.Description == "" || tool.Annotations == nil {
			t.Errorf("missing tool documentation: %s", tool.Name)
		}
		write := tool.Name == "create_page" || tool.Name == "append_page"
		if tool.Annotations.ReadOnlyHint == write {
			t.Errorf("wrong annotation: %s", tool.Name)
		}
	}
	cases := []struct {
		name string
		args map[string]any
		fail bool
	}{
		{"convert_to_cosense", map[string]any{"content": "**日本語**", "format": "markdown"}, false},
		{"convert_to_cosense", map[string]any{"content": "text", "format": "text"}, true},
		{"convert_to_cosense", map[string]any{"content": "text"}, true},
		{"convert_to_cosense", map[string]any{"content": 1, "format": "html"}, true},
		{"get_page", map[string]any{"title": ""}, true},
		{"search_pages", map[string]any{"query": " "}, true},
		{"list_pages", map[string]any{"skip": -1}, true},
		{"list_pages", map[string]any{"limit": 1001}, true},
		{"list_pages", map[string]any{"limit": 0}, true},
		{"append_page", map[string]any{"title": "Test", "content": "body", "format": "cosense"}, true},
		{"append_page", map[string]any{"title": "Test", "content": "body", "format": "cosense", "expected_commit_id": "invalid"}, true},
		{"create_page", map[string]any{"title": "Test", "content": "<script>only()</script>", "format": "html"}, true},
		{"create_page", map[string]any{"title": "Test", "content": "body", "format": "cosense"}, true}, // No credentials: no request.
	}
	for _, tt := range cases {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tt.name, Arguments: tt.args})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError != tt.fail {
			t.Errorf("%s %v: %+v", tt.name, tt.args, res)
		}
		if !tt.fail && res.StructuredContent == nil {
			t.Error("missing conversion result")
		}
	}
}
