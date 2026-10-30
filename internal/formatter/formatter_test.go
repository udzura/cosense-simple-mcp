package formatter

import (
	"strings"
	"testing"
)

func TestConversion(t *testing.T) {
	cases := []struct {
		name, format, input string
		contains            []string
		absent              []string
		warning             bool
	}{
		{"markdown basics", "markdown", "# 日本語 🚀\n\n段落 **太字** と *斜体*。\n\n- 一つ\n  - 二つ", []string{"[**** 日本語 🚀]", "[* 太字]", "[/ 斜体]", " 一つ", "  二つ"}, nil, true},
		{"links", "markdown", "[Go](https://go.dev/?a=1&b=2) [知識](知識) [別名](Target)", []string{"[https://go.dev/?a=1&b=2 Go]", "[知識]", "別名 [Target]"}, nil, true},
		{"code", "markdown", "```ruby\nputs '日本語'\n  puts '[not a link]'\n```\n\nUse `a < b & c`.", []string{"code:ruby\n puts '日本語'\n   puts '[not a link]'", "`a < b & c`"}, nil, false},
		{"table", "markdown", "| 名前 | A | 空 |\n| --- | --- | --- |\n| 日本語 | a\\|b | |\n| **太字** | [Go](https://go.dev) | `x` |", []string{"table:table-1\n 名前\tA\t空\n 日本語\ta|b\t\n [* 太字]\t[https://go.dev Go]\t`x`"}, []string{"---"}, false},
		{"html", "html", "<h2>日本語</h2><p>Hello &amp; &#x1F680;<br>次へ <a href='https://example.com'>link</a></p><ul><li>一つ<ul><li>二つ</li></ul></li></ul>", []string{"[*** 日本語]", "Hello & 🚀\n次へ [https://example.com link]", " 一つ", "  二つ"}, nil, true},
		{"html table", "html", "<table><tr><th>A</th><th>B</th></tr><tr><td></td><td>日本語</td></tr></table>", []string{"table:table-1\n A\tB\n \t日本語"}, nil, false},
		{"unsafe table", "html", "<table><tr><th colspan='2'>見出し</th></tr><tr><td>A<br>B</td><td>C</td></tr></table>", []string{"Row 1", "Cell 1: 見出し", "Cell 1: A\n  B", "Cell 2: C"}, []string{"table:table-"}, true},
		{"unsafe elements", "html", "<p>safe<script>secret()</script><style>.secret{}</style><u>readable</u></p>", []string{"safereadable"}, []string{"secret"}, true},
		{"entities", "markdown", "&lt;hello&gt; &amp; &#x65E5;&#26412;", []string{"<hello> & 日本"}, nil, false},
		{"unsupported link", "html", "<a href='javascript:alert(1)'>label</a>", []string{"label (javascript:alert(1))"}, nil, true},
		{"inline backtick", "html", "<code>a`b</code>", []string{"`a\\`b`"}, nil, false},
		{"raw html markdown", "markdown", "<div>visible<script>hidden</script><b>bold</b></div>", []string{"visible[* bold]"}, []string{"hidden"}, true},
		{"literal brackets", "markdown", `\[literal\]`, []string{"［literal］"}, nil, true},
		{"ordered", "html", "<ol start='3'><li>three</li><li>four</li></ol>", []string{" 3. three\n 4. four"}, nil, false},
		{"wrapped list", "html", "<div>\n<ul>\n<li>one</li>\n<li>two</li>\n</ul>\n</div>", []string{" one\n two"}, nil, false},
		{"code blank lines", "markdown", "```text\n\nfirst\n\nlast\n```", []string{"code:text\n \n first\n \n last"}, nil, false},
		{"complex link label", "markdown", "[**Go**](https://go.dev)", []string{"[https://go.dev Go]"}, []string{"[* Go]"}, true},
		{"nested table", "html", "<table><tr><td>outer<table><tr><td>inner</td></tr></table></td></tr></table>", []string{"Row 1", "outer", "inner"}, nil, true},
		{"images and rules", "markdown", "![alt](https://example.com/a.png)\n\n---", []string{"[https://example.com/a.png alt]", "———"}, nil, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Convert(tt.input, tt.format)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got.Text, want) {
					t.Errorf("missing %q in %q", want, got.Text)
				}
			}
			for _, bad := range tt.absent {
				if strings.Contains(got.Text, bad) {
					t.Errorf("unexpected %q in %q", bad, got.Text)
				}
			}
			if (len(got.Warnings) > 0) != tt.warning {
				t.Errorf("warnings = %v", got.Warnings)
			}
		})
	}
}

func TestExactTableLayout(t *testing.T) {
	got, err := Convert("| A | B |\n|---|---|\n| | |", "markdown")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "table:table-1\n A\tB\n \t" {
		t.Fatalf("table structure changed: %q", got.Text)
	}
}

func TestCosenseVerbatim(t *testing.T) {
	s := "\n[日本語] \r\ncode:go\n \t[not a link]\n\n"
	got, err := Convert(s, "cosense")
	if err != nil || got.Text != s || len(got.Warnings) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestInputErrors(t *testing.T) {
	for _, tt := range []struct{ content, format string }{{"ok", "text"}, {strings.Repeat("x", MaxInputBytes+1), "markdown"}, {"\xff", "html"}, {"\x00", "cosense"}} {
		if _, err := Convert(tt.content, tt.format); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
}

func TestTableEmptyAndComplexCells(t *testing.T) {
	in := "| A | B | C |\n|---|---|---|\n| | | |\n| **太字** and *斜体* | `a\\|b` | [別名](日本語) |"
	r, err := Convert(in, "markdown")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text, "\n \t\t\n") || !strings.Contains(r.Text, "`a|b`") || !strings.Contains(r.Text, "別名 [日本語]") {
		t.Fatalf("%q", r.Text)
	}
}
