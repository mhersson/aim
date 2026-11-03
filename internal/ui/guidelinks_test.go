package ui

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"unicode"
)

var (
	guideLinkRE    = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	guideAttrRE    = regexp.MustCompile(`\b(?:src|href)="([^"]+)"`)
	guideMentionRE = regexp.MustCompile(`docs/guide/([\p{L}\p{N}_-]+\.md)(?:#([\p{L}\p{N}_-]+))?`)
	guideLabelRE   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	guideSchemeRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// githubSlug returns the anchor GitHub gives a heading with the given text.
func githubSlug(heading string) string {
	s := guideLabelRE.ReplaceAllString(heading, "$1")
	s = strings.ReplaceAll(s, "`", "")

	var b strings.Builder

	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}

	return b.String()
}

// stripCodeSpans blanks out backtick-delimited code spans in a line.
func stripCodeSpans(line string) string {
	var b strings.Builder

	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++

			continue
		}

		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}

		end := strings.Index(line[i+n:], strings.Repeat("`", n))
		if end < 0 {
			b.WriteString(line[i : i+n])
			i += n

			continue
		}

		i += n + end + n
	}

	return b.String()
}

// guideLines returns the lines of a page outside fenced code blocks.
func guideLines(src string) []string {
	var out []string

	fenced := false

	for line := range strings.SplitSeq(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced

			continue
		}

		if !fenced {
			out = append(out, line)
		}
	}

	return out
}

// pageAnchors returns the anchors of the headings of a Markdown page.
func pageAnchors(src string) []string {
	seen := map[string]int{}

	var anchors []string

	for _, line := range guideLines(src) {
		rest, ok := strings.CutPrefix(line, "#")
		if !ok {
			continue
		}

		rest = strings.TrimLeft(rest, "#")
		if !strings.HasPrefix(rest, " ") {
			continue
		}

		slug := githubSlug(strings.TrimRight(strings.TrimSpace(rest), "# "))
		if n := seen[slug]; n > 0 {
			anchors = append(anchors, fmt.Sprintf("%s-%d", slug, n))
		} else {
			anchors = append(anchors, slug)
		}

		seen[slug]++
	}

	return anchors
}

// brokenLinks lists the links of docs/guide/*.md, README.md and
// docs/architecture.md (Markdown link targets and HTML src and href attributes)
// and the guide mentions of config.example.toml that do not resolve in a
// repository-rooted fsys.
func brokenLinks(fsys fs.FS) ([]string, error) {
	var problems []string

	anchorsOf := map[string][]string{}
	check := func(from, file, anchor string) error {
		if file != "" {
			if _, err := fs.Stat(fsys, file); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %s does not exist", from, file))

				return nil
			}
		}

		if anchor == "" || !strings.HasSuffix(file, ".md") {
			return nil
		}

		anchors, ok := anchorsOf[file]
		if !ok {
			src, err := fs.ReadFile(fsys, file)
			if err != nil {
				return fmt.Errorf("read %s: %w", file, err)
			}

			anchors = pageAnchors(string(src))
			anchorsOf[file] = anchors
		}

		if !slices.Contains(anchors, anchor) {
			problems = append(problems, fmt.Sprintf("%s: %s#%s has no such heading", from, file, anchor))
		}

		return nil
	}

	pages, err := fs.Glob(fsys, "docs/guide/*.md")
	if err != nil {
		return nil, fmt.Errorf("glob guides: %w", err)
	}

	for _, page := range []string{"README.md", "docs/architecture.md"} {
		if _, err := fs.Stat(fsys, page); err == nil {
			pages = append(pages, page)
		}
	}

	for _, page := range pages {
		src, err := fs.ReadFile(fsys, page)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", page, err)
		}

		for _, line := range guideLines(string(src)) {
			line = stripCodeSpans(line)
			matches := append(guideLinkRE.FindAllStringSubmatch(line, -1), guideAttrRE.FindAllStringSubmatch(line, -1)...)

			for _, m := range matches {
				target := m[1]
				if guideSchemeRE.MatchString(target) {
					continue
				}

				p, anchor, _ := strings.Cut(target, "#")

				file := page
				if p != "" {
					file = path.Join(path.Dir(page), p)
				}

				if err := check(page+" -> "+target, file, anchor); err != nil {
					return nil, err
				}
			}
		}
	}

	if src, err := fs.ReadFile(fsys, "config.example.toml"); err == nil {
		for _, m := range guideMentionRE.FindAllStringSubmatch(string(src), -1) {
			target := "docs/guide/" + m[1]
			if m[2] != "" {
				target += "#" + m[2]
			}

			if err := check("config.example.toml -> "+target, "docs/guide/"+m[1], m[2]); err != nil {
				return nil, err
			}
		}
	}

	return problems, nil
}

func TestGitHubSlug(t *testing.T) {
	t.Parallel()

	tests := []struct{ heading, want string }{
		{"Follow the agent's edits", "follow-the-agents-edits"},
		{"Copy, paste and the mouse", "copy-paste-and-the-mouse"},
		{"The `[keys]` tables", "the-keys-tables"},
		{"PR list", "pr-list"},
		{"See [Keys](keys.md) now", "see-keys-now"},
	}
	for _, tt := range tests {
		t.Run(tt.heading, func(t *testing.T) {
			t.Parallel()

			if got := githubSlug(tt.heading); got != tt.want {
				t.Errorf("githubSlug(%q) = %q, want %q", tt.heading, got, tt.want)
			}
		})
	}
}

func TestBrokenLinks(t *testing.T) {
	t.Parallel()

	const target = "# Target\n\n## Follow the agent's edits\n\n## Same\n\n## Same\n\n```\n## Hidden\n```\n"

	tests := []struct {
		name   string
		page   string
		readme string
		arch   string
		files  []string
		toml   string
		want   []string
	}{
		{name: "good link and anchor", page: "[a](target.md) [b](target.md#follow-the-agents-edits) [c](#own)\n\n## Own\n"},
		{name: "missing file", page: "[a](nope.md)\n", want: []string{"a.md", "nope.md"}},
		{name: "missing anchor", page: "[a](target.md#gone)\n", want: []string{"a.md", "target.md#gone"}},
		{name: "bare anchor missing", page: "[a](#gone)\n", want: []string{"a.md", "#gone"}},
		{name: "toml mention of a missing anchor", page: "x\n", toml: "## See docs/guide/target.md#gone\n", want: []string{"config.example.toml", "target.md#gone"}},
		{name: "toml mention before a period", page: "x\n", toml: "## See docs/guide/target.md#same.\n"},
		{name: "toml mention of a missing anchor before a period", page: "x\n", toml: "## See docs/guide/target.md#gone.\n", want: []string{"target.md#gone"}},
		{name: "toml mention of a missing file", page: "x\n", toml: "## docs/guide/nope.md.\n", want: []string{"nope.md"}},
		{name: "scheme link", page: "[a](https://example.com/x#y) [b](mailto:a@b.c)\n"},
		{name: "link in a fence", page: "```\n[a](nope.md)\n```\n"},
		{name: "link in a code span", page: "see `[a](nope.md)` here\n"},
		{name: "repeated heading", page: "[a](target.md#same) [b](target.md#same-1)\n"},
		{name: "repeated heading past the last", page: "[a](target.md#same-2)\n", want: []string{"target.md#same-2"}},
		{name: "heading in a fence", page: "[a](target.md#hidden)\n", want: []string{"target.md#hidden"}},
		{name: "non-markdown target", page: "[a](../../config.example.toml#x)\n", toml: "x\n"},
		{name: "readme good link and anchors", page: "x\n", readme: "[a](docs/guide/target.md#same) [b](#own) <a href=\"LICENSE\">l</a>\n\n## Own\n", files: []string{"LICENSE"}},
		{name: "readme missing file", page: "x\n", readme: "[a](docs/nope.md)\n", want: []string{"README.md", "docs/nope.md"}},
		{name: "readme image missing", page: "x\n", readme: "<td><img src=\"docs/images/y.png\"></td>\n", want: []string{"README.md", "docs/images/y.png"}},
		{name: "architecture good links and anchors", page: "x\n", arch: "[a](guide/target.md#same) [b](#own) [c](../LICENSE)\n\n## Own\n", files: []string{"LICENSE"}},
		{name: "architecture missing file", page: "x\n", arch: "[a](nope.md)\n", want: []string{"docs/architecture.md", "docs/nope.md"}},
		{name: "guide html href missing", page: "<a href=\"nope.md\">n</a>\n", want: []string{"a.md", "nope.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := fstest.MapFS{
				"docs/guide/a.md":      {Data: []byte(tt.page)},
				"docs/guide/target.md": {Data: []byte(target)},
			}
			if tt.readme != "" {
				fsys["README.md"] = &fstest.MapFile{Data: []byte(tt.readme)}
			}

			if tt.arch != "" {
				fsys["docs/architecture.md"] = &fstest.MapFile{Data: []byte(tt.arch)}
			}

			for _, f := range tt.files {
				fsys[f] = &fstest.MapFile{}
			}

			if tt.toml != "" {
				fsys["config.example.toml"] = &fstest.MapFile{Data: []byte(tt.toml)}
			}

			got, err := brokenLinks(fsys)
			if err != nil {
				t.Fatal(err)
			}

			if len(tt.want) == 0 {
				if len(got) != 0 {
					t.Fatalf("brokenLinks = %q, want none", got)
				}

				return
			}

			if len(got) != 1 {
				t.Fatalf("brokenLinks = %q, want one problem", got)
			}

			for _, w := range tt.want {
				if !strings.Contains(got[0], w) {
					t.Errorf("problem %q does not name %q", got[0], w)
				}
			}
		})
	}
}

func TestGuideLinks(t *testing.T) {
	t.Parallel()

	got, err := brokenLinks(os.DirFS("../.."))
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range got {
		t.Error(p)
	}
}
