package cosense

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var objectID = regexp.MustCompile(`^[0-9a-f]{24}$`)
var lineID = regexp.MustCompile(`^[0-9a-f]{24,32}$`)

type WriteResult struct {
	URL      string   `json:"url"`
	CommitID string   `json:"commit_id"`
	Warnings []string `json:"warnings"`
}

type commit struct {
	Kind      string           `json:"kind"`
	ProjectID string           `json:"projectId"`
	PageID    string           `json:"pageId"`
	ParentID  *string          `json:"parentId"`
	UserID    string           `json:"userId"`
	Changes   []map[string]any `json:"changes"`
	Cursor    any              `json:"cursor"`
	Freeze    bool             `json:"freeze"`
}

func (c *Client) CreatePage(ctx context.Context, title, content string) (WriteResult, error) {
	return c.write(ctx, title, content, "", true)
}

// AppendPage requires the caller's reviewed revision. Replaying an old request
// fails the revision check, including after a lost response or process restart.
func (c *Client) AppendPage(ctx context.Context, title, content, expectedCommitID string) (WriteResult, error) {
	if !objectID.MatchString(expectedCommitID) {
		return WriteResult{}, errors.New("expected_commit_id must be the commitId returned by get_page")
	}
	return c.write(ctx, title, content, expectedCommitID, false)
}

func (c *Client) write(ctx context.Context, title, content, expected string, create bool) (WriteResult, error) {
	var out WriteResult
	if err := ValidateTitle(title); err != nil {
		return out, err
	}
	if strings.TrimSpace(content) == "" || len(content) > 1<<20 || !utf8.ValidString(content) || strings.ContainsAny(content, "\x00\r\u2028\u2029") {
		return out, errors.New("write content must be nonempty UTF-8, at most 1 MiB, with LF line endings and without NUL or Unicode line separators")
	}
	if strings.Count(content, "\n") >= 10000 {
		return out, errors.New("write content must have at most 10000 lines")
	}
	if c.sid == "" {
		return out, errors.New("COSENSE_SID is required for writes")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	select {
	case c.writes <- struct{}{}:
		defer func() { <-c.writes }()
	case <-ctx.Done():
		return out, ctx.Err()
	}
	p, err := c.fetchPage(ctx, title)
	if err != nil {
		return out, err
	} // A 404 is not enough to manufacture a new-page object.
	if create && *p.Persistent {
		return out, ErrExists
	}
	if !create && !*p.Persistent {
		return out, ErrNotFound
	}
	if !create && p.CommitID != expected {
		return out, ErrConflict
	}
	if !objectID.MatchString(p.ID) || (*p.Persistent && !objectID.MatchString(p.CommitID)) {
		return out, errors.New("unsupported page revision metadata")
	}
	for _, l := range p.Lines {
		if !lineID.MatchString(l.ID) {
			return out, errors.New("unsupported page line metadata")
		}
	}
	if create && (p.CommitID != "" || len(p.Lines) != 1 || p.Lines[0].Text != p.Title) {
		return out, errors.New("unsupported new-page template; no write attempted")
	}
	var user, project struct {
		ID string `json:"id"`
	}
	if err = c.get(ctx, "/api/users/me", &user); err != nil {
		return out, err
	}
	if !objectID.MatchString(user.ID) {
		return out, errors.New("Cosense session is not logged in")
	}
	if err = c.get(ctx, "/api/projects/"+c.project, &project); err != nil {
		return out, err
	}
	if !objectID.MatchString(project.ID) {
		return out, errors.New("unsupported project metadata")
	}
	ws, err := c.connect(ctx)
	if err != nil {
		return out, err
	}
	defer ws.CloseNow()
	// Re-read after connection setup. parentId also guards the final race on the server.
	latest, err := c.fetchPage(ctx, title)
	if err != nil {
		return out, err
	}
	if create && *latest.Persistent {
		return out, ErrExists
	}
	if !create && (latest.ID != p.ID || latest.CommitID != p.CommitID || !reflect.DeepEqual(latest.Lines, p.Lines)) {
		return out, ErrConflict
	}
	// Nonpersistent templates may have newly allocated IDs on each GET. Use the
	// original server-provided template; duplicate-title rejection guards creation.
	changes := []map[string]any{}
	if create {
		changes = append(changes, map[string]any{"title": p.Title})
	}
	added := strings.Split(content, "\n")
	// A blank unindented line prevents an appended indented block from extending
	// the previous code/table block. Existing lines remain byte-for-byte untouched.
	if !create {
		added = append([]string{""}, added...)
	}
	for _, text := range added {
		id, err := newLineID(user.ID)
		if err != nil {
			return out, err
		}
		changes = append(changes, map[string]any{"_insert": "_end", "lines": Line{ID: id, Text: text}})
	}
	all := p.Content + "\n" + strings.Join(added, "\n")
	changes = append(changes, map[string]any{"linesCount": len(p.Lines) + len(added)}, map[string]any{"charsCount": utf8.RuneCountInString(strings.ReplaceAll(all, "\n", ""))})
	// Only add common links; never replace existing graph metadata with a partial parse.
	links, projectLinks := collectLinks(content, p.Links, p.ProjectLinks)
	if !reflect.DeepEqual(links, p.Links) {
		changes = append(changes, map[string]any{"links": links})
	}
	if !reflect.DeepEqual(projectLinks, p.ProjectLinks) {
		changes = append(changes, map[string]any{"projectLinks": projectLinks})
	}
	if create {
		desc := []string{}
		for _, l := range added {
			if s := strings.TrimSpace(l); s != "" && len(desc) < 5 {
				r := []rune(s)
				desc = append(desc, string(r[:min(200, len(r))]))
			}
		}
		changes = append(changes, map[string]any{"descriptions": desc})
	}
	var parent *string
	if p.CommitID != "" {
		parent = &p.CommitID
	}
	id, err := sendCommit(ctx, ws, commit{Kind: "page", ProjectID: project.ID, PageID: p.ID, ParentID: parent, UserID: user.ID, Changes: changes, Freeze: true})
	if err != nil {
		return out, err
	}
	out = WriteResult{URL: c.PageURL(p.Title), CommitID: id, Warnings: []string{"Writes use an undocumented Socket.IO protocol. Advanced link, image, file, and infobox metadata is not fully regenerated; review the page in Cosense."}}
	// Success means both an acknowledgement and a read-back with all original
	// line IDs/text intact plus our appended lines. Never attempt rollback.
	check, err := c.GetPage(ctx, p.Title)
	if err != nil || check.ID != p.ID || len(check.Lines) < len(p.Lines)+len(added) {
		return out, ErrUncertain
	}
	if !reflect.DeepEqual(check.Lines[:len(p.Lines)], p.Lines) {
		return out, ErrUncertain
	}
	for i, text := range added {
		if check.Lines[len(p.Lines)+i].Text != text {
			return out, ErrUncertain
		}
	}
	return out, nil
}

func newLineID(userID string) (string, error) {
	var b [7]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("cannot generate line identifier")
	}
	return fmt.Sprintf("%08x%s0000%s", time.Now().Unix(), userID[len(userID)-6:], hex.EncodeToString(b[:])), nil
}

// collectLinks deliberately recognizes only common bracket links, skipping code
// blocks/spans. Unrecognized metadata is left intact and reported as a warning.
func collectLinks(text string, links, projects []string) ([]string, []string) {
	links = slices.Clone(links)
	projects = slices.Clone(projects)
	add := func(dst *[]string, s string) {
		for _, v := range *dst {
			if strings.EqualFold(strings.ReplaceAll(v, " ", "_"), strings.ReplaceAll(s, " ", "_")) {
				return
			}
		}
		*dst = append(*dst, s)
	}
	codeDepth := -1
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimLeft(line, " \t")
		depth := len(line) - len(trim)
		if codeDepth >= 0 && depth > codeDepth {
			continue
		}
		codeDepth = -1
		if strings.HasPrefix(trim, "code:") {
			codeDepth = depth
			continue
		}
		inCode := false
		for i := 0; i < len(line); i++ {
			if line[i] == '`' && (i == 0 || line[i-1] != '\\') {
				inCode = !inCode
				continue
			}
			if inCode || line[i] != '[' {
				continue
			}
			j := strings.IndexByte(line[i+1:], ']')
			if j < 0 {
				continue
			}
			s := line[i+1 : i+1+j]
			if s == "" || strings.ContainsAny(s, "[\t") || strings.Contains(s, "://") || strings.Contains(s, "`") || strings.ContainsAny(s[:1], "*!$#") || strings.HasPrefix(s, "/ ") || strings.HasPrefix(s, "- ") || strings.HasSuffix(s, ".icon") {
				continue
			}
			if strings.HasPrefix(s, "/") {
				if strings.Count(s, "/") >= 2 {
					add(&projects, s)
				}
			} else {
				add(&links, s)
			}
		}
	}
	return links, projects
}
