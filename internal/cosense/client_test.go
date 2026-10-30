package cosense

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, err := New("test-project", "secret-cookie")
	if err != nil {
		t.Fatal(err)
	}
	c.baseURL = s.URL
	return c
}
func TestReadEndpoints(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("connect.sid")
		if err != nil || cookie.Value != "secret-cookie" {
			t.Error("missing authentication")
		}
		switch r.URL.Path {
		case "/api/pages/test-project":
			if r.URL.Query().Get("skip") != "2" || r.URL.Query().Get("limit") != "10" {
				t.Error("pagination")
			}
			json.NewEncoder(w).Encode(PageList{Count: 3, Skip: 2, Limit: 10, Pages: []PageSummary{{Title: "日本語 /?#%"}}})
		case "/api/pages/test-project/search/query":
			if r.URL.Query().Get("q") != "日本語 & Go" {
				t.Error("query encoding")
			}
			json.NewEncoder(w).Encode(SearchResult{Count: 1, Limit: 100, Pages: []PageSummary{{Title: "日本語"}}})
		default:
			if !strings.Contains(r.URL.EscapedPath(), "%2F%3F%23%25") || r.URL.Query().Get("followRename") != "false" {
				t.Errorf("unsafe URL: %s", r.URL.EscapedPath())
			}
			p := fixturePage(true)
			json.NewEncoder(w).Encode(p)
		}
	})
	if out, err := c.ListPages(context.Background(), 2, 10); err != nil || len(out.Pages) != 1 || !strings.Contains(out.Pages[0].URL, "%2F%3F%23%25") {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := c.SearchPages(context.Background(), "日本語 & Go"); err != nil {
		t.Fatal(err)
	}
	if out, err := c.GetPage(context.Background(), "日本語 /?#%"); err != nil || out.Content != "Test\noriginal [link]" {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestAPIErrors(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(status)
				w.Write([]byte("secret-cookie and private body"))
			})
			_, err := c.GetPage(context.Background(), "Test")
			var api *APIError
			if !errors.As(err, &api) || api.Status != status || strings.Contains(err.Error(), "secret") {
				t.Fatalf("%v", err)
			}
			if status == 429 && !strings.Contains(err.Error(), "60 seconds") {
				t.Fatal(err)
			}
		})
	}
}
func TestBadResponses(t *testing.T) {
	for _, body := range []string{"<html>login</html>", `{}`, `{"id":"x","title":"Test","lines":[{"id":"x","text":"Test"}]}`, strings.Repeat("x", maxResponseBytes+1)} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
		if _, err := c.GetPage(context.Background(), "Test"); err == nil {
			t.Fatal("accepted incomplete response")
		}
	}
}
func TestNoRedirects(t *testing.T) {
	visited := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visited = true }))
	defer target.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	if _, err := c.GetPage(context.Background(), "Test"); err == nil || visited {
		t.Fatal("followed redirect")
	}
}
func TestCancellation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.GetPage(ctx, "Test"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestPublicReadAndValidation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("unexpected cookie")
		}
		json.NewEncoder(w).Encode(fixturePage(true))
	})
	c.sid = ""
	if _, err := c.GetPage(context.Background(), "Test"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreatePage(context.Background(), "Test", "body"); err == nil {
		t.Fatal("unauthenticated write")
	}
	for _, title := range []string{"", "\n", "..", "bad[title]", "line\u2028break"} {
		if ValidateTitle(title) == nil {
			t.Errorf("accepted %q", title)
		}
	}
	if _, err := New("https://evil.test", "secret"); err == nil {
		t.Fatal("accepted URL")
	}
	if _, err := New("valid", "a; injected=b"); err == nil {
		t.Fatal("accepted invalid cookie")
	}
}
