package formatter

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

func htmlDocument(s string) (ConversionResult, error) {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return ConversionResult{}, fmt.Errorf("invalid HTML")
	}
	r := &renderer{warnings: []string{}}
	text := strings.Trim(r.node(doc), "\n")
	return ConversionResult{Text: text, Warnings: r.warnings}, nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func (r *renderer) children(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" &&
			(c.PrevSibling == nil || c.NextSibling == nil || blockElement(c.PrevSibling) || blockElement(c.NextSibling)) {
			continue
		}
		b.WriteString(r.node(c))
	}
	return b.String()
}

func blockElement(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch n.Data {
	case "p", "div", "section", "article", "main", "header", "footer", "aside", "figure", "figcaption", "details", "summary", "dl", "dt", "dd", "h1", "h2", "h3", "h4", "h5", "h6", "pre", "ul", "ol", "li", "table", "blockquote", "hr":
		return true
	}
	return false
}

func (r *renderer) node(n *html.Node) string {
	if n.Type == html.TextNode {
		// Collapse HTML whitespace, but retain spaces between inline elements.
		var b strings.Builder
		space := false
		for _, ch := range n.Data {
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' {
				if !space {
					b.WriteByte(' ')
					space = true
				}
			} else {
				b.WriteRune(ch)
				space = false
			}
		}
		return r.literal(b.String())
	}
	if n.Type == html.CommentNode {
		return ""
	}
	if n.Type != html.ElementNode {
		return r.children(n)
	}
	switch n.Data {
	case "script", "style", "template":
		r.warn("Script, style, and template elements and their contents were discarded.")
		return ""
	case "head":
		return ""
	case "html", "body":
		return r.children(n)
	case "br":
		return "\n"
	case "p", "div", "section", "article", "main", "header", "footer", "aside", "figure", "figcaption", "details", "summary", "dl", "dt", "dd":
		return "\n" + strings.Trim(r.children(n), "\n") + "\n"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level, _ := strconv.Atoi(n.Data[1:])
		r.warn("Headings use Cosense text-size decoration; Cosense has no semantic heading levels.")
		return "\n[" + strings.Repeat("*", max(1, 5-level)) + " " + strings.TrimSpace(r.children(n)) + "]\n"
	case "strong", "b":
		return r.decorate(n, "*")
	case "em", "i":
		return r.decorate(n, "/")
	case "del", "s":
		return r.decorate(n, "-")
	case "a":
		label := strings.TrimSpace(r.children(n))
		if strings.ContainsAny(label, "[]") {
			r.warn("Formatting inside a hyperlink label was reduced to plain text.")
			label = r.literal(strings.TrimSpace(rawText(n)))
		}
		return r.link(label, attr(n, "href"))
	case "img":
		r.warn("Images were converted to links with alternative text.")
		return r.link(r.literal(attr(n, "alt")), attr(n, "src"))
	case "pre":
		lang := "text"
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Data == "code" {
				for _, class := range strings.Fields(attr(c, "class")) {
					if strings.HasPrefix(class, "language-") {
						lang = strings.TrimPrefix(class, "language-")
					}
				}
			}
		}
		if strings.ContainsFunc(lang, func(c rune) bool {
			return !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune("._+-", c)
		}) {
			lang = "text"
			r.warn("Unsupported code language label was replaced with text.")
		}
		return "\ncode:" + lang + "\n" + indent(strings.TrimSuffix(rawText(n), "\n"), 1) + "\n"
	case "code":
		return "`" + strings.ReplaceAll(rawText(n), "`", "\\`") + "`"
	case "ul", "ol":
		var items []string
		number := 1
		if v, err := strconv.Atoi(attr(n, "start")); err == nil {
			number = v
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.Data != "li" {
				continue
			}
			item := strings.Trim(r.children(c), " \n")
			if n.Data == "ol" {
				item = fmt.Sprintf("%d. %s", number, item)
				number++
			}
			items = append(items, indent(item, 1))
		}
		return "\n" + strings.Join(items, "\n") + "\n"
	case "blockquote":
		lines := strings.Split(strings.TrimSpace(r.children(n)), "\n")
		for i := range lines {
			lines[i] = "> " + lines[i]
		}
		return "\n" + strings.Join(lines, "\n") + "\n"
	case "table":
		return r.table(n)
	case "hr":
		r.warn("Horizontal rules were converted to a plain divider.")
		return "\n———\n"
	case "input":
		if attr(n, "type") == "checkbox" {
			for _, a := range n.Attr {
				if a.Key == "checked" {
					return "☑ "
				}
			}
			return "☐ "
		}
		r.warn("Unsupported form elements were discarded.")
		return ""
	case "span", "li", "caption", "tbody", "thead", "tfoot", "tr", "td", "th":
		return r.children(n)
	default:
		r.warn("Unsupported HTML formatting was reduced to its text and child elements.")
		return r.children(n)
	}
}

func rawText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "template") {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(rawText(c))
	}
	return b.String()
}

func (r *renderer) decorate(n *html.Node, style string) string {
	s := strings.TrimSpace(r.children(n))
	if strings.Contains(s, "\n") {
		r.warn("Multiline inline decoration was flattened.")
		return s
	}
	return "[" + style + " " + s + "]"
}

func (r *renderer) link(label, dest string) string {
	if dest == "" {
		return label
	}
	u, err := url.Parse(dest)
	if err != nil {
		r.warn("An invalid link was preserved as text.")
		return label + " (" + r.literal(dest) + ")"
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		r.warn("A non-HTTP link was preserved as text.")
		return label + " (" + r.literal(dest) + ")"
	}
	if u.Scheme == "" && u.Host == "" {
		target, err := url.PathUnescape(dest)
		if err != nil {
			target = dest
		}
		if strings.HasPrefix(target, "./") {
			target = strings.TrimPrefix(target, "./")
		}
		if strings.HasPrefix(target, "#") || strings.ContainsAny(target, "[]\r\n") {
			r.warn("An internal link could not be represented safely and was preserved as text.")
			return label + " (" + r.literal(target) + ")"
		}
		if label == "" || label == target {
			return "[" + target + "]"
		}
		r.warn("Internal link labels are shown before the target because Cosense has no Markdown-style aliases.")
		return label + " [" + target + "]"
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	dest = strings.NewReplacer("[", "%5B", "]", "%5D", " ", "%20").Replace(u.String())
	label = strings.Join(strings.Fields(label), " ")
	if label == "" {
		label = "link"
	}
	return "[" + dest + " " + label + "]"
}

func (r *renderer) table(n *html.Node) string {
	r.tables++
	name := fmt.Sprintf("table-%d", r.tables)
	var rows [][]string
	unsafe := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node != n && node.Type == html.ElementNode && node.Data == "table" {
			unsafe = true
			return
		}
		if node.Type == html.ElementNode && node.Data == "tr" {
			var row []string
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				if c.Type != html.ElementNode || (c.Data != "td" && c.Data != "th") {
					continue
				}
				for _, key := range []string{"colspan", "rowspan"} {
					if v := attr(c, key); v != "" && v != "1" {
						unsafe = true
					}
				}
				s := strings.TrimSpace(r.children(c))
				if strings.ContainsAny(s, "\t\n\r") {
					unsafe = true
				}
				row = append(row, s)
			}
			rows = append(rows, row)
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	width := 0
	for _, row := range rows {
		if width == 0 {
			width = len(row)
		}
		if len(row) != width {
			unsafe = true
		}
	}
	caption := ""
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Data == "caption" {
			caption = strings.TrimSpace(r.children(c)) + "\n"
		}
	}
	if unsafe {
		r.warn("A table with merged, multiline, nested, or uneven cells was converted to a row/cell list; spans and visual alignment are not preserved.")
		var b strings.Builder
		b.WriteString("\n" + caption + name + "\n")
		for i, row := range rows {
			fmt.Fprintf(&b, " Row %d\n", i+1)
			for j, s := range row {
				fmt.Fprintf(&b, "%s\n", indent(fmt.Sprintf("Cell %d: %s", j+1, s), 2))
			}
		}
		return b.String()
	}
	var b strings.Builder
	b.WriteString("\n" + caption + "table:" + name + "\n")
	for _, row := range rows {
		b.WriteString(" " + strings.Join(row, "\t") + "\n")
	}
	return b.String()
}
