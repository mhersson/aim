package md

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yuin/goldmark/v2/ast"
	east "github.com/yuin/goldmark/v2/extension/ast"
)

// shape renders a block tree compactly so tests can compare whole structures.
func shape(d *Doc) string {
	var b strings.Builder
	shapeChildren(&b, d, d.root)

	return b.String()
}

func shapeChildren(b *strings.Builder, d *Doc, n ast.Node) {
	first := true
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if !first {
			b.WriteByte(' ')
		}

		first = false

		shapeNode(b, d, c)
	}
}

func shapeNode(b *strings.Builder, d *Doc, n ast.Node) {
	group := func(name string) {
		b.WriteString(name + "{")
		shapeChildren(b, d, n)
		b.WriteString("}")
	}

	switch n := n.(type) {
	case *foldNode:
		open := ""
		if n.Open {
			open = "+"
		}

		group(fmt.Sprintf("fold#%d%s(%s)", n.ID, open, n.Summary))
	case *alertNode:
		group("alert:" + n.Alert.String())
	case *htmlFragment:
		fmt.Fprintf(b, "frag(%q)", n.HTML)
	case *ast.Paragraph:
		b.WriteString("p(" + inlineText(d, n) + ")")
	case *ast.HTMLBlock:
		b.WriteString("html")
	case *ast.Blockquote:
		group("quote")
	case *ast.List:
		group("list")
	case *ast.ListItem:
		group("li")
	default:
		b.WriteString(n.Kind().String())
	}
}

func inlineText(d *Doc, n ast.Node) string {
	var b strings.Builder

	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.WriteString(t.Value.Value(d.src))

			if t.SoftLineBreak() {
				b.WriteByte('\n')
			}

			continue
		}

		b.WriteString("<" + c.Kind().String() + ">")
	}

	return strings.TrimRight(b.String(), "\n")
}

func foldDepth(n ast.Node) int {
	deepest := 0
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		deepest = max(deepest, foldDepth(c))
	}

	if _, ok := n.(*foldNode); ok {
		deepest++
	}

	return deepest
}

func TestParseCutsAtMaxBytes(t *testing.T) {
	t.Parallel()

	line := strings.Repeat("x", 99) + "\n"
	long := strings.Repeat(line, (MaxBytes+10)/len(line)+1)[:MaxBytes+10]

	tests := []struct {
		name    string
		src     string
		wantCut bool
		wantLen int
	}{
		{"exactly the bound", long[:MaxBytes], false, MaxBytes},
		{"past the bound cuts at the last line break", long, true, strings.LastIndexByte(long[:MaxBytes], '\n')},
		{"no line break backs off to a rune start", strings.Repeat("€", MaxBytes/3+10), true, MaxBytes / 3 * 3},
		{"no line break in ascii cuts at the bound", strings.Repeat("a", MaxBytes+10), true, MaxBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Parse([]byte(tt.src), ParseOptions{})
			if d.Cut() != tt.wantCut {
				t.Fatalf("Cut() = %v, want %v", d.Cut(), tt.wantCut)
			}

			if len(d.src) != tt.wantLen {
				t.Fatalf("kept %d bytes, want %d", len(d.src), tt.wantLen)
			}

			if !strings.HasPrefix(tt.src, string(d.src)) || !utf8.Valid(d.src) {
				t.Fatalf("kept source is not a valid prefix of the input")
			}

			if tt.wantCut && strings.Contains(tt.name, "last line break") && tt.src[len(d.src)] != '\n' {
				t.Fatalf("cut is not at a line break")
			}
		})
	}
}

func TestParseDoesNotAliasInput(t *testing.T) {
	t.Parallel()

	src := []byte("hello")
	d := Parse(src, ParseOptions{})

	src[0] = 'j'
	if string(d.src) != "hello" {
		t.Fatalf("Doc source changed with the caller's slice: %q", d.src)
	}
}

func TestParseGroupsDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		src   string
		want  string
		folds int
	}{
		{
			name:  "summary and body blocks",
			src:   "<details>\n<summary>Gate</summary>\n\nbody\n\n</details>\n",
			want:  `fold#0(Gate){p(body)}`,
			folds: 1,
		},
		{
			name:  "no summary reads Details",
			src:   "<details>\n\nbody\n\n</details>\n",
			want:  `fold#0(Details){p(body)}`,
			folds: 1,
		},
		{
			name:  "summary in the block after the opener",
			src:   "<details>\n\n<summary>S</summary>\n\nbody\n\n</details>\n",
			want:  `fold#0(S){p(body)}`,
			folds: 1,
		},
		{
			name:  "a later summary does not rename the fold",
			src:   "<details>\n\nbody\n\n<summary>late</summary>\n\n</details>\n",
			want:  `fold#0(Details){p(body) html}`,
			folds: 1,
		},
		{
			name:  "summary entities and tags",
			src:   "<details>\n<summary>  A &amp;\n  <b>B</b> </summary>\n\nx\n\n</details>\n",
			want:  `fold#0(A & B){p(x)}`,
			folds: 1,
		},
		{
			name:  "inline tags in a summary add no space",
			src:   "<details><summary>Fix <code>x</code>. a<b>b</b>c</summary>y</details>\n",
			want:  `fold#0(Fix x. abc){frag("y")}`,
			folds: 1,
		},
		{
			name:  "br and div in a summary separate words",
			src:   "<details><summary>a<br>b<div>c</div>d</summary>y</details>\n",
			want:  `fold#0(a b c d){frag("y")}`,
			folds: 1,
		},
		{
			// A browser reads an unclosed summary to the end of its block,
			// so the later tags are summary text, not structure.
			name:  "unclosed summary swallows later tags in the block",
			src:   "<details><summary>s<details>x</details>\n\nafter\n",
			want:  `fold#0(sx){p(after)}`,
			folds: 1,
		},
		{
			name:  "tags are case-insensitive and attributes allowed",
			src:   "<DETAILS open class=\"a\"><SUMMARY>Up</SUMMARY>\n\nx\n\n</DETAILS>\n",
			want:  `fold#0+(Up){p(x)}`,
			folds: 1,
		},
		{
			name:  "open attribute spellings",
			src:   "<details OPEN=\"\"><summary>a</summary>x</details>\n<details class=x open='open'><summary>b</summary>y</details>\n<details open=open><summary>c</summary>z</details>\n",
			want:  `fold#0+(a){frag("x")} fold#1+(b){frag("y")} fold#2+(c){frag("z")}`,
			folds: 3,
		},
		{
			name:  "open only inside another attribute value is not open",
			src:   "<details title=\"open\" data-open><summary>a</summary>x</details>\n",
			want:  `fold#0(a){frag("x")}`,
			folds: 1,
		},
		{
			name:  "one-line form keeps its text as a fragment",
			src:   "<details><summary>s</summary>body</details>\n",
			want:  `fold#0(s){frag("body")}`,
			folds: 1,
		},
		{
			name:  "back to back folds in one block",
			src:   "<details><summary>a</summary>x</details>\n<details><summary>b</summary>y</details>\n",
			want:  `fold#0(a){frag("x")} fold#1(b){frag("y")}`,
			folds: 2,
		},
		{
			name:  "missing close runs to the end",
			src:   "<details>\n<summary>A</summary>\n\nx\n\ny\n",
			want:  `fold#0(A){p(x) p(y)}`,
			folds: 1,
		},
		{
			name:  "nested across blocks",
			src:   "<details>\n<summary>Out</summary>\n\n<details>\n<summary>In</summary>\n\nbody\n\n</details>\n\nafter\n\n</details>\n\nend\n",
			want:  `fold#0(Out){fold#1(In){p(body)} p(after)} p(end)`,
			folds: 2,
		},
		{
			name:  "nested in one block",
			src:   "<details open><summary>a</summary><details><summary>b</summary>x</details>y</details>\n",
			want:  `fold#0+(a){fold#1(b){frag("x")} frag("y")}`,
			folds: 2,
		},
		{
			name:  "close in a later block than the opener",
			src:   "<details><summary>a</summary>x\n\nmid\n\n</details>y\n\nend\n",
			want:  `fold#0(a){frag("x") p(mid)} frag("y") p(end)`,
			folds: 1,
		},
		{
			name:  "stray close is dropped",
			src:   "a\n\n</details>\n\nb\n",
			want:  `p(a) p(b)`,
			folds: 0,
		},
		{
			name:  "other html blocks stay",
			src:   "<p align=\"center\">\n\nx\n\n</p>\n",
			want:  `html p(x) html`,
			folds: 0,
		},
		{
			name:  "longer tag names are not details",
			src:   "<detailsfoo>\n\nx\n",
			want:  `html p(x)`,
			folds: 0,
		},
		{
			name:  "a commented-out opener is not a fold",
			src:   "<!-- <details> -->\n\nx\n",
			want:  `html p(x)`,
			folds: 0,
		},
		{
			name:  "inside a list item",
			src:   "- item\n\n  <details>\n  <summary>S</summary>\n\n  body\n\n  </details>\n",
			want:  `list{li{p(item) fold#0(S){p(body)}}}`,
			folds: 1,
		},
		{
			name:  "inside a quote",
			src:   "> <details>\n> <summary>S</summary>\n>\n> body\n>\n> </details>\n",
			want:  `quote{fold#0(S){p(body)}}`,
			folds: 1,
		},
		{
			name:  "an open fold ends with its container",
			src:   "- <details>\n  <summary>S</summary>\n\n  body\n\nafter\n",
			want:  `list{li{fold#0(S){p(body)}}} p(after)`,
			folds: 1,
		},
		{
			name:  "closer glued to an image paragraph",
			src:   "<details>\n<summary>Screenshot</summary>\n\n![shot](https://e.com/s.png)</details>\n\n## After\n\nMore text.\n",
			want:  `fold#0(Screenshot){p(<Image>)} Heading p(More text.)`,
			folds: 1,
		},
		{
			name:  "closer glued to text",
			src:   "<details>\n<summary>S</summary>\n\nsome log text</details>\n\nafter\n",
			want:  `fold#0(S){p(some log text)} p(after)`,
			folds: 1,
		},
		{
			name:  "text glued after a closer",
			src:   "<details>\n<summary>S</summary>\n\nbody</details>text\n\nafter\n",
			want:  `fold#0(S){p(body)} p(text) p(after)`,
			folds: 1,
		},
		{
			name:  "text after a closer that opens its line stays a fragment",
			src:   "<details>\n<summary>S</summary>\n\nbody\n\n</details>text\n",
			want:  `fold#0(S){p(body)} frag("text")`,
			folds: 1,
		},
		{
			name:  "a whole details inline in a paragraph",
			src:   "before <details><summary>s</summary>body</details> after\n",
			want:  `p(before ) fold#0(s){p(body)} p( after)`,
			folds: 1,
		},
		{
			name:  "an inline opener names and opens its fold",
			src:   "x <details open><summary>  A &amp;\n <b>B</b> </summary>y</details>\n",
			want:  `p(x ) fold#0+(A & B){p(y)}`,
			folds: 1,
		},
		{
			name:  "an inline summary with no close runs to the end of the paragraph",
			src:   "x <details><summary>one <i>two</i></details> three\n",
			want:  `p(x ) fold#0(one two three){}`,
			folds: 1,
		},
		{
			name:  "an inline opener with nothing after it leaves an empty fold",
			src:   "text <details>\n",
			want:  `p(text ) fold#0(Details){}`,
			folds: 1,
		},
		{
			name:  "inline folds nest in one paragraph",
			src:   "a <details><summary>o</summary>b <details><summary>i</summary>c</details> d</details> e\n",
			want:  `p(a ) fold#0(o){p(b ) fold#1(i){p(c)} p( d)} p( e)`,
			folds: 2,
		},
		{
			name:  "trailing closer inside a list item",
			src:   "- <details>\n  <summary>S</summary>\n\n  log</details>\n\nafter\n",
			want:  `list{li{fold#0(S){p(log)}}} p(after)`,
			folds: 1,
		},
		{
			name:  "a closer in a nested container closes nothing outside it",
			src:   "<details>\n<summary>S</summary>\n\n> quoted</details>\n\nafter\n",
			want:  `fold#0(S){quote{p(quoted)} p(after)}`,
			folds: 1,
		},
		{
			name:  "a stray closer glued to text is dropped",
			src:   "one</details>two\n",
			want:  `p(one) p(two)`,
			folds: 0,
		},
		{
			name:  "a quoted attribute holding a bracket keeps its tag whole",
			src:   "<details title=\"a>b\" open>\n<summary>S</summary>\n\nbody\n\n</details>\n",
			want:  `fold#0+(S){p(body)}`,
			folds: 1,
		},
		{
			name:  "closer inside strong closes the fold after its paragraph",
			src:   "<details>\n<summary>S</summary>\n\n**log</details>**\n\n## After\n\nMore text.\n",
			want:  `fold#0(S){p(<Strong>)} Heading p(More text.)`,
			folds: 1,
		},
		{
			name:  "closer inside link text closes the fold after its paragraph",
			src:   "<details>\n<summary>S</summary>\n\n[log</details>](https://e.com)\n\n## After\n\nMore text.\n",
			want:  `fold#0(S){p(<Link>)} Heading p(More text.)`,
			folds: 1,
		},
		{
			name:  "closer inside emphasis in a list item",
			src:   "- <details>\n  <summary>S</summary>\n\n  *log</details>*\n\nafter\n",
			want:  `list{li{fold#0(S){p(<Emphasis>)}}} p(after)`,
			folds: 1,
		},
		{
			name:  "a direct closer then a nested one close two folds",
			src:   "<details>\n<summary>O</summary>\n\n<details>\n<summary>I</summary>\n\nx</details> y **z</details>**\n\nafter\n",
			want:  `fold#0(O){fold#1(I){p(x)} p( y <Strong>)} p(after)`,
			folds: 2,
		},
		{
			name:  "two nested closers close two folds",
			src:   "<details>\n<summary>O</summary>\n\n<details>\n<summary>I</summary>\n\n**a</details>** *b</details>*\n\nafter\n",
			want:  `fold#0(O){fold#1(I){p(<Strong> <Emphasis>)}} p(after)`,
			folds: 2,
		},
		{
			name:  "a nested closer closes after the whole part of its paragraph",
			src:   "a <details><summary>s</summary>b **c</details>** d\n\nafter\n",
			want:  `p(a ) fold#0(s){p(b <Strong> d)} p(after)`,
			folds: 1,
		},
		{
			name:  "a nested closer with no fold open is dropped",
			src:   "plain **text</details>** more\n\nafter\n",
			want:  `p(plain <Strong> more) p(after)`,
			folds: 0,
		},
		{
			name:  "a nested opener is not read",
			src:   "x **y<details>** z\n",
			want:  `p(x <Strong> z)`,
			folds: 0,
		},
		{
			name:  "a nested closer in a nested container closes nothing outside it",
			src:   "<details>\n<summary>S</summary>\n\n> **q</details>**\n\nafter\n",
			want:  `fold#0(S){quote{p(<Strong>)} p(after)}`,
			folds: 1,
		},
		{
			name:  "ids follow document order across containers",
			src:   "<details><summary>a</summary>x</details>\n\n- <details><summary>b</summary>y</details>\n\n<details><summary>c</summary>z</details>\n",
			want:  `fold#0(a){frag("x")} list{li{fold#1(b){frag("y")}}} fold#2(c){frag("z")}`,
			folds: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Parse([]byte(tt.src), ParseOptions{})
			if got := shape(d); got != tt.want {
				t.Errorf("shape:\n got %s\nwant %s", got, tt.want)
			}

			if d.folds != tt.folds {
				t.Errorf("folds = %d, want %d", d.folds, tt.folds)
			}
		})
	}
}

func TestParseDetailsNestedTenDeep(t *testing.T) {
	t.Parallel()

	const depth = 10

	inOneBlock := strings.Repeat("<details><summary>s</summary>", depth) + "core" + strings.Repeat("</details>", depth) + "\n"

	var across strings.Builder
	for i := range depth {
		fmt.Fprintf(&across, "<details>\n<summary>s%d</summary>\n\n", i)
	}

	across.WriteString("core\n\n")
	across.WriteString(strings.Repeat("</details>\n\n", depth))

	for name, src := range map[string]string{"one block": inOneBlock, "across blocks": across.String()} {
		t.Run(name, func(t *testing.T) {
			d := Parse([]byte(src), ParseOptions{})
			if d.folds != depth {
				t.Fatalf("folds = %d, want %d", d.folds, depth)
			}

			if got := foldDepth(d.root); got != depth {
				t.Fatalf("fold depth = %d, want %d", got, depth)
			}

			if got := len(foldIDs(d.root)); got != depth {
				t.Fatalf("fold nodes = %d, want %d", got, depth)
			}
		})
	}
}

func TestParseDetailsNeverClosed(t *testing.T) {
	t.Parallel()

	src := strings.Repeat("<details>\n<summary>s</summary>\n\ntext\n\n", 50)

	d := Parse([]byte(src), ParseOptions{})
	if d.folds != 50 {
		t.Fatalf("folds = %d, want 50", d.folds)
	}

	if got := foldDepth(d.root); got != 50 {
		t.Fatalf("fold depth = %d, want 50", got)
	}

	ids := foldIDs(d.root)
	for i, id := range ids {
		if id != i {
			t.Fatalf("fold ids in pre-order = %v", ids)
		}
	}
}

func foldIDs(root ast.Node) []int {
	var ids []int

	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if f, ok := n.(*foldNode); ok && entering {
			ids = append(ids, f.ID)
		}

		return ast.WalkContinue, nil
	})

	return ids
}

func TestParseFindsAlerts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"note", "> [!NOTE]\n> body\n", `alert:note{p(body)}`},
		{"tip", "> [!TIP]\n> body\n", `alert:tip{p(body)}`},
		{"important", "> [!IMPORTANT]\n> body\n", `alert:important{p(body)}`},
		{"warning", "> [!WARNING]\n> body\n", `alert:warning{p(body)}`},
		{"caution", "> [!CAUTION]\n> body\n", `alert:caution{p(body)}`},
		{"lower case", "> [!tip]\n> body\n", `alert:tip{p(body)}`},
		{"mixed case", "> [!Important]\n> body\n", `alert:important{p(body)}`},
		{"surrounding spaces", ">   [!NOTE]  \n> body\n", `alert:note{p(body)}`},
		{"marker alone", "> [!NOTE]\n", `alert:note{}`},
		{"later paragraph stays", "> [!NOTE]\n>\n> two\n", `alert:note{p(two)}`},
		{"body keeps its lines", "> [!NOTE]\n> a\n> b\n", "alert:note{p(a\nb)}"},
		{"blocks after the marker", "> [!NOTE]\n> a\n>\n> - x\n", `alert:note{p(a) list{li{p(x)}}}`},
		{"text after the marker is a quote", "> [!NOTE] text\n> body\n", "quote{p([!NOTE] text\nbody)}"},
		{"marker on a later line is a quote", "> intro\n> [!NOTE]\n> body\n", "quote{p(intro\n[!NOTE]\nbody)}"},
		{"marker in a later paragraph is a quote", "> intro\n>\n> [!NOTE]\n", `quote{p(intro) p([!NOTE])}`},
		{"unknown marker is a quote", "> [!BOGUS]\n> body\n", "quote{p([!BOGUS]\nbody)}"},
		{"styled marker is a quote", "> **[!NOTE]**\n> body\n", "quote{p(<Strong>\nbody)}"},
		{"plain quote", "> hello\n", `quote{p(hello)}`},
		{"alert in a list item", "- > [!TIP]\n  > body\n", `list{li{alert:tip{p(body)}}}`},
		{
			"alert inside a fold",
			"<details>\n<summary>S</summary>\n\n> [!WARNING]\n> body\n\n</details>\n",
			`fold#0(S){alert:warning{p(body)}}`,
		},
		{"nested quote alert", "> > [!NOTE]\n> > body\n", `quote{alert:note{p(body)}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Parse([]byte(tt.src), ParseOptions{})
			if got := shape(d); got != tt.want {
				t.Errorf("shape:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestParseNeverPanics(t *testing.T) {
	t.Parallel()

	inputs := map[string]string{
		"only close":            "</details>",
		"unterminated tag":      "<details",
		"unterminated summary":  "<details><summary>x",
		"stray summary":         "<summary>",
		"unterminated comment":  "<details><!-- x",
		"invalid utf8":          "\xff\xfe<details>\xc3(\n\n</details>\x80",
		"invalid utf8 summary":  "<details><summary>\xff\xfe</summary></details>",
		"nul bytes":             "\x00<details>\x00\n> [!NOTE]\x00\n",
		"alert marker only":     "> [!NOTE]",
		"many details openers":  strings.Repeat("<details>", 600),
		"many details no close": strings.Repeat("<details ", 600),
	}
	for name, src := range inputs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := Parse([]byte(src), ParseOptions{Base: "https://example.test/x/"})
			if d == nil || d.root == nil {
				t.Fatal("Parse returned no document")
			}

			if d.Cut() {
				t.Fatal("small input reported as cut")
			}
		})
	}
}

// TestParseRecoveryEmptiesTheDoc runs the handler parse defers over a Doc whose
// parse finished, by panicking inside a function that defers it.
func TestParseRecoveryEmptiesTheDoc(t *testing.T) {
	t.Parallel()

	d := Parse([]byte("<details><summary>s</summary>x</details>\n\nref[^1]\n\n[^1]: note\n"), ParseOptions{})
	if d.folds == 0 || len(d.notes.labels) == 0 || len(d.notes.defs) == 0 || len(d.Layout(LayoutOptions{Width: 40}).Lines) == 0 {
		t.Fatalf("the parsed doc is empty: folds %d, notes %+v", d.folds, d.notes)
	}

	func() {
		defer d.recoverParse()

		panic("a goldmark failure")
	}()

	if d.root.HasChildren() || d.folds != 0 {
		t.Errorf("root has children %v, folds %d", d.root.HasChildren(), d.folds)
	}

	if len(d.notes.labels) != 0 || len(d.notes.defs) != 0 {
		t.Errorf("note index kept %d labels and %d definitions", len(d.notes.labels), len(d.notes.defs))
	}

	if l := d.Layout(LayoutOptions{Width: 40}); len(l.Lines) != 0 || len(l.Folds) != 0 {
		t.Errorf("layout of the emptied doc = %q", l.Plain())
	}
}

func TestParseClassifiesBase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base     string
		wantKind baseKind
		wantRef  string
	}{
		{"", baseNone, ""},
		{"https://example.test/a/", baseURL, "https://example.test/a/"},
		{"http://example.test", baseURL, "http://example.test"},
		{"HTTPS://example.test/a", baseURL, "https://example.test/a"},
		{"ftp://example.test/a", baseNone, ""},
		{"https:///a", baseNone, ""},
		{"example.test/a", baseNone, ""},
		{"mailto:a@b.test", baseNone, ""},
		{"relative/dir", baseNone, ""},
		{"/abs/dir/", baseLocal, "/abs/dir"},
		{"/abs/../dir", baseLocal, "/dir"},
		{"%zz", baseNone, ""},
	}
	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			d := Parse([]byte("x"), ParseOptions{Base: tt.base})
			if d.base.kind != tt.wantKind {
				t.Fatalf("kind = %v, want %v", d.base.kind, tt.wantKind)
			}

			got := ""

			switch d.base.kind {
			case baseURL:
				got = d.base.url.String()
			case baseLocal:
				got = d.base.dir
			}

			if got != tt.wantRef {
				t.Fatalf("base = %q, want %q", got, tt.wantRef)
			}
		})
	}
}

func TestParseKeepsFootnoteRegistry(t *testing.T) {
	t.Parallel()

	d := Parse([]byte("a[^1] b[^x]\n\n[^1]: note\n"), ParseOptions{})
	if d.footnotes == nil {
		t.Fatal("no footnote registry")
	}

	if d.footnotes.FindByLabel([]byte("1")) == nil {
		t.Error("definition 1 not found by label")
	}

	if d.footnotes.FindByLabel([]byte("x")) != nil {
		t.Error("undefined label found")
	}

	var refs []int

	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if r, ok := n.(*east.FootnoteReference); ok && entering {
			refs = append(refs, r.Index)
		}

		return ast.WalkContinue, nil
	})

	if len(refs) != 1 || refs[0] != 1 {
		t.Errorf("reference indexes = %v, want [1]", refs)
	}
}
