// Package cosense isolates Cosense's undocumented REST and Socket.IO protocols.
package cosense

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const origin = "https://scrapbox.io"
const maxResponseBytes = 8 << 20

var (
	ErrExists    = errors.New("page already exists; use get_page then append_page")
	ErrNotFound  = errors.New("page does not exist or is not accessible")
	ErrConflict  = errors.New("page changed; read it again and review before appending")
	ErrUncertain = errors.New("write outcome is uncertain; do not retry automatically; read the page and inspect its history first")
)

type APIError struct {
	Status     int
	RetryAfter string
}

func (e *APIError) Error() string {
	switch e.Status {
	case 401, 403:
		return "Cosense authentication or permission denied; check COSENSE_SID and project membership"
	case 404:
		return ErrNotFound.Error()
	case 429:
		if e.RetryAfter != "" {
			return "Cosense rate limit exceeded; retry after " + e.RetryAfter
		}
		return "Cosense rate limit exceeded; retry later"
	default:
		return fmt.Sprintf("Cosense HTTP error (%d)", e.Status)
	}
}

type Client struct {
	project string
	sid     string
	baseURL string
	http    *http.Client
	writes  chan struct{}
}

func New(project, sid string) (*Client, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`).MatchString(project) || len(project) > 100 {
		return nil, errors.New("COSENSE_PROJECT_NAME must be a project name, not a URL")
	}
	if sid != "" {
		cookie := &http.Cookie{Name: "connect.sid", Value: sid}
		if cookie.Valid() != nil {
			return nil, errors.New("COSENSE_SID must contain only the session cookie value")
		}
	}
	return &Client{project: project, sid: sid, baseURL: origin, http: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, writes: make(chan struct{}, 1)}, nil
}

func ValidateTitle(title string) error {
	if strings.TrimSpace(title) == "" || len(title) > 1000 || !utf8.ValidString(title) || strings.ContainsAny(title, "[]") || strings.ContainsFunc(title, unicode.IsControl) || strings.ContainsAny(title, "\u2028\u2029") || title == "." || title == ".." {
		return errors.New("title must be nonempty UTF-8, at most 1000 bytes, without brackets or control characters")
	}
	return nil
}

func titlePath(title string) string { return url.PathEscape(strings.ReplaceAll(title, " ", "_")) }
func (c *Client) PageURL(title string) string {
	return origin + "/" + c.project + "/" + titlePath(title)
}

func (c *Client) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return errors.New("cannot construct Cosense request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	if c.sid != "" {
		req.AddCookie(&http.Cookie{Name: "connect.sid", Value: c.sid})
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Cosense request failed or timed out") // Never echo URLs or response bodies.
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		retry := ""
		if s := res.Header.Get("Retry-After"); s != "" {
			if n, e := strconv.ParseUint(s, 10, 32); e == nil {
				retry = fmt.Sprintf("%d seconds", n)
			} else if t, e := http.ParseTime(s); e == nil {
				retry = t.UTC().Format(time.RFC3339)
			}
		}
		return &APIError{Status: res.StatusCode, RetryAfter: retry}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil || len(b) > maxResponseBytes {
		return errors.New("Cosense response unreadable or exceeds 8 MiB")
	}
	if json.Unmarshal(b, dst) != nil {
		return errors.New("unexpected Cosense JSON response")
	}
	return nil
}

type Line struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type Page struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	CommitID     string   `json:"commitId"`
	Persistent   *bool    `json:"persistent"`
	Lines        []Line   `json:"lines"`
	Links        []string `json:"links"`
	ProjectLinks []string `json:"projectLinks"`
	Descriptions []string `json:"descriptions"`
	URL          string   `json:"url"`
	Content      string   `json:"content"`
}

func (c *Client) fetchPage(ctx context.Context, title string) (Page, error) {
	var p Page
	if err := ValidateTitle(title); err != nil {
		return p, err
	}
	err := c.get(ctx, "/api/pages/"+c.project+"/"+titlePath(title)+"?followRename=false", &p)
	if err != nil {
		return p, err
	}
	if p.ID == "" || p.Persistent == nil || len(p.Lines) == 0 || p.Title == "" {
		return p, errors.New("incomplete Cosense page response; refusing to infer existence")
	}
	var lines []string
	for _, l := range p.Lines {
		lines = append(lines, l.Text)
	}
	p.Content = strings.Join(lines, "\n") // Includes the title line, as returned by Cosense.
	p.URL = c.PageURL(p.Title)
	return p, nil
}

func (c *Client) GetPage(ctx context.Context, title string) (Page, error) {
	p, err := c.fetchPage(ctx, title)
	if err == nil && !*p.Persistent {
		err = ErrNotFound
	}
	return p, err
}

type PageSummary struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Descriptions []string `json:"descriptions,omitempty"`
	Lines        []string `json:"lines,omitempty"`
	Updated      int64    `json:"updated"`
	URL          string   `json:"url"`
}
type PageList struct {
	Count int           `json:"count"`
	Skip  int           `json:"skip"`
	Limit int           `json:"limit"`
	Pages []PageSummary `json:"pages"`
}

func (c *Client) ListPages(ctx context.Context, skip, limit int) (PageList, error) {
	var out PageList
	if skip < 0 || limit < 1 || limit > 1000 {
		return out, errors.New("skip must be nonnegative and limit must be 1..1000")
	}
	err := c.get(ctx, fmt.Sprintf("/api/pages/%s?skip=%d&limit=%d", c.project, skip, limit), &out)
	if err == nil && out.Pages == nil {
		err = errors.New("incomplete Cosense page list response")
	}
	for i := range out.Pages {
		out.Pages[i].URL = c.PageURL(out.Pages[i].Title)
	}
	return out, err
}

type SearchResult struct {
	Query string        `json:"searchQuery"`
	Count int           `json:"count"`
	Limit int           `json:"limit"`
	Pages []PageSummary `json:"pages"`
}

func (c *Client) SearchPages(ctx context.Context, query string) (SearchResult, error) {
	var out SearchResult
	if strings.TrimSpace(query) == "" || len(query) > 2000 || !utf8.ValidString(query) {
		return out, errors.New("query must be nonempty UTF-8, at most 2000 bytes")
	}
	err := c.get(ctx, "/api/pages/"+c.project+"/search/query?"+url.Values{"q": {query}}.Encode(), &out)
	if err == nil && out.Pages == nil {
		err = errors.New("incomplete Cosense search response")
	}
	for i := range out.Pages {
		out.Pages[i].URL = c.PageURL(out.Pages[i].Title)
	}
	return out, err
}
