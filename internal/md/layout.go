package md

import (
	"slices"
	"strings"

	"github.com/yuin/goldmark/v2/ast"
	east "github.com/yuin/goldmark/v2/extension/ast"
)

const (
	// maxWidth is the widest line a layout produces, whatever width is asked for.
	maxWidth = 100
	// maxNest is the depth past which lists and quotes add no more indentation.
	maxNest = 16
	// maxDepth is the deepest the layout recurses into nested containers; flat
	// lays out what nests deeper without recursion.
	maxDepth = 32
)

// Role says what a run is for. The UI maps each role to theme colours; the
// layout never carries a colour.
type Role uint8

const (
	RoleText Role = iota
	RoleHeading1
	RoleHeading2
	RoleHeading3
	RoleRule
	RoleCode
	RoleLink
	RoleKey
	RoleBullet
	RoleTaskDone
	RoleTaskDoneText
	RoleTaskOpen
	RoleCodeFrame
	RoleCodeLang
	RoleLineNumber
	RoleCodeText
	RoleHidden
	RoleQuoteBar
	RoleQuoteText
	RoleAlertNote
	RoleAlertTip
	RoleAlertImportant
	RoleAlertWarning
	RoleAlertCaution
	RoleSummaryMark
	RoleSummary
	RoleFoldHint
	RoleImage
	RoleImageHost
	RolePicture
	RoleTableFrame
	RoleTableHeader
	RoleFootnoteRef
	RoleFootnoteRule
	RoleFootnoteNumber
	RoleFootnoteText
)

// Highlighter classifies the bytes of code lines for RoleCodeText runs; nil
// leaves code plain.
type Highlighter func(lang string, lines []string) [][]Span

// Span is a highlighted byte range of one code line.
type Span struct {
	Start, End int
	Class      uint8 // the highlighter's own class
}

// LayoutOptions configures Layout.
type LayoutOptions struct {
	Width     int          // cells; at most 100 are used
	FoldKey   *string      // the key a closed fold names; "" names none, nil is "enter"
	Open      map[int]bool // by Fold.ID; a present key overrides the fold's default
	Icons     bool
	Highlight Highlighter
	// Picture sizes the picture an image shows, given its source and the cells
	// its line has; nil, or a size with no cells, leaves an image line.
	Picture func(src string, maxCols int) PictureSize
}

// PictureSize is what Picture answers: a picture when Cols and Rows are both
// above zero, else an image line, with a loading note when Loading.
type PictureSize struct {
	Cols, Rows int
	ID         uint32 // the caller's, copied into Image.ID
	Loading    bool
}

// Layout is a document laid out for one width.
type Layout struct {
	Lines []Line
	// Source holds, for each line, the source lines that made it; the zero value
	// for a line no source made.
	Source []SourceRange
	Links  []Link
	Images []Image
	Folds  []Fold
}

// Line is one screen line, never wider than the width laid out for.
type Line []Run

// Run is a stretch of text with one style.
type Run struct {
	Text      string
	Role      Role
	Bold      bool
	Italic    bool
	Underline bool
	Strike    bool
	Class     uint8  // the highlighter's class, for RoleCodeText
	Link      int    // index into Layout.Links, -1 for none
	Path      string // a relative link resolved against a local base

	chrome bool   // drawn, never copied
	tab    bool   // a code line's tab, drawn as spaces
	rest   string // on the … that cuts a code line: the line from the first cell the cut dropped
	join   Join   // on the first run of a wrapped continuation
}

// Link is a link target the UI may emit as OSC 8: always http, https or mailto.
type Link struct{ URL string }

// Image is an image reference on a layout line. For a picture, Line is its
// first row and Rows, Cols and ID are set; an image line leaves them zero.
type Image struct {
	Line       int
	Alt, Src   string // Src is resolved for loading
	Dest       string // the destination as written, for copy
	Link       int
	Rows, Cols int
	ID         uint32
}

// Fold is a foldable block.
type Fold struct {
	ID, Line int
	Open     bool // the state laid out, after LayoutOptions.Open
}

// Plain returns each line's text, for search and copy; a picture row gives an
// empty string.
func (l Layout) Plain() []string {
	out := make([]string, len(l.Lines))

	for i, line := range l.Lines {
		if slices.ContainsFunc(line, func(r Run) bool { return r.Role == RolePicture }) {
			continue
		}

		var b strings.Builder
		for _, r := range line {
			b.WriteString(r.Text)
		}

		out[i] = b.String()
	}

	return out
}

// layouter holds the state of one Layout call; the Doc stays untouched.
type layouter struct {
	d         *Doc
	width     int
	open      map[int]bool
	icons     bool
	highlight Highlighter
	picture   func(src string, maxCols int) PictureSize
	foldKey   string
	out       Layout
}

// blockCtx is what a block inherits from the containers around it.
type blockCtx struct {
	text      Role // the role of RoleText runs
	depth     int  // lists and quotes entered
	listDepth int  // lists entered
	done      bool // the next paragraph is a done task's text
	// lead is the width of the marker a capped list item will draw before the first
	// line this block appends; that line is narrower by it.
	lead int
}

// frag collects lines with the images and folds on them. Blocks append to it
// and indexes are absolute, so nesting copies no lines.
type frag struct {
	lines  []Line
	images []Image
	folds  []Fold
	// src is the source range of each line; it may be shorter than lines, and
	// the lines past it have none yet (see sync).
	src []SourceRange
}

// Layout wraps the document to o.Width cells (at most 100, at least 1).
func (d *Doc) Layout(o LayoutOptions) Layout {
	l := &layouter{d: d, width: min(max(o.Width, 1), maxWidth), open: o.Open, icons: o.Icons, highlight: o.Highlight, picture: o.Picture, foldKey: "enter"}
	if o.FoldKey != nil {
		l.foldKey = *o.FoldKey
	}

	var f frag

	l.blocks(&f, d.root, l.width, blockCtx{}, false)
	l.footnotes(&f, l.width)

	f.stamp(0, SourceRange{})

	l.out.Lines, l.out.Source, l.out.Images, l.out.Folds = f.lines, f.src, f.images, f.folds
	l.dropUnusedLinks()

	return l.out
}

// blocks appends the children of parent laid out in width cells, a blank line
// apart unless compact.
func (l *layouter) blocks(out *frag, parent ast.Node, width int, c blockCtx, compact bool) {
	start := len(out.lines)

	for ch := parent.FirstChild(); ch != nil; ch = ch.NextSibling() {
		before := len(out.lines)
		sep := before > start && !compact

		if sep {
			out.lines = append(out.lines, nil)
			out.markNone(before)
		}

		l.block(out, ch, width, c)

		if sep {
			dropBlank(out, before)
		}

		if len(out.lines) > start {
			c.lead = 0
		}
	}
}

// dropBlank removes the separator line at index at when the block after it
// drew nothing.
func dropBlank(out *frag, at int) {
	if len(out.lines) == at+1 && out.lines[at] == nil {
		out.lines = out.lines[:at]
		out.src = out.src[:min(at, len(out.src))]
	}
}

// block appends one block; its lines that nothing inside it claimed carry its
// source range.
func (l *layouter) block(out *frag, n ast.Node, width int, c blockCtx) {
	from := len(out.lines)

	l.blockLines(out, n, width, c)
	out.stamp(from, l.d.ranges[n])
}

func (l *layouter) blockLines(out *frag, n ast.Node, width int, c blockCtx) {
	text := c.text
	if c.done {
		text = RoleTaskDoneText
	}

	c.done = false

	if c.depth >= maxDepth && nests(n) {
		l.flat(out, n, width, c)

		return
	}

	switch n := n.(type) {
	case *ast.Paragraph:
		l.flow(out, restyle(l.inlines(n), text, false), width, c.lead)
	case *ast.Heading:
		l.heading(out, n, width, c.lead)
	case *ast.List:
		l.list(out, n, width, c)
	case *ast.Blockquote:
		l.quote(out, n, width, c)
	case *alertNode:
		l.alert(out, n, width, c)
	case *foldNode:
		l.fold(out, n, width, c)
	case *htmlFragment:
		l.flow(out, restyle(l.htmlSegs(n.HTML), text, false), width, c.lead)
	case *ast.HTMLBlock:
		l.htmlBlock(out, n, width, text, c.lead)
	case *ast.CodeBlock:
		l.codeBlock(out, n, max(width-c.lead, 1))
	case *east.Table:
		l.table(out, n, max(width-c.lead, 1), text)
	case *ast.ThematicBreak:
		out.lines = append(out.lines, l.rule(RoleRule, max(width-c.lead, 1)))
	}
}

// rule is a line of n cells of ─.
func (l *layouter) rule(role Role, n int) Line {
	return Line{styled(role).run(strings.Repeat("─", n))}
}

func styled(role Role) style { return style{role: role, link: -1} }

// restyle gives the RoleText segs the role text, and every seg bold when bold.
func restyle(segs []seg, text Role, bold bool) []seg {
	for i := range segs {
		if segs[i].img != nil || segs[i].brk {
			continue
		}

		if segs[i].st.role == RoleText {
			segs[i].st.role = text
		}

		if bold {
			segs[i].st.bold = true
		}
	}

	return segs
}

// flow wraps segs, giving each image a line of its own. The first line it
// appends is lead cells narrower.
func (l *layouter) flow(out *frag, segs []seg, width, lead int) {
	start := 0

	for i, sg := range segs {
		if sg.img == nil {
			continue
		}

		if lines := wrapLead(segs[start:i], width, lead); len(lines) > 0 {
			out.lines = append(out.lines, lines...)
			lead = 0
		}

		start = afterImageBreak(segs, i+1)

		l.image(out, sg, max(width-lead, 1))

		lead = 0
	}

	out.lines = append(out.lines, wrapLead(segs[start:], width, lead)...)
}

// image appends the rows of one image: a picture when l.picture sizes one, else
// the image line, with a loading note when asked, fit to cols cells.
func (l *layouter) image(out *frag, sg seg, cols int) {
	im := Image{Line: len(out.lines), Alt: sg.img.alt, Src: sg.img.src, Dest: sg.img.dest, Link: sg.st.link}

	var size PictureSize
	if l.picture != nil && sg.img.src != "" {
		size = l.picture(sg.img.src, cols)
	}

	if size.Cols > 0 && size.Rows > 0 {
		im.Rows, im.Cols, im.ID = size.Rows, min(size.Cols, cols), size.ID

		st := styled(RolePicture)
		st.link = sg.st.link
		row := Line{st.run(strings.Repeat(" ", im.Cols))}

		for range size.Rows {
			out.lines = append(out.lines, slices.Clone(row))
		}
	} else {
		line := l.imageLine(sg)
		if size.Loading {
			line = append(line, styled(RoleImageHost).run("  loading…"))
		}

		out.lines = append(out.lines, fit(line, cols))
	}

	out.images = append(out.images, im)
}

// afterImageBreak returns where the text after an image starts: past the first
// hard break that follows it (and the spaces before the break), since the image
// already ended its line. A second break still makes a blank line.
func afterImageBreak(segs []seg, from int) int {
	j := from
	for j < len(segs) && segs[j].img == nil && !segs[j].brk && strings.TrimSpace(segs[j].text) == "" {
		j++
	}

	if j < len(segs) && segs[j].brk && !segs[j].soft {
		return j + 1
	}

	return from
}

func (l *layouter) heading(out *frag, h *ast.Heading, width, lead int) {
	role := RoleHeading3

	switch h.Level {
	case 1:
		role = RoleHeading1
	case 2:
		role = RoleHeading2
	}

	start := len(out.lines)

	l.flow(out, restyle(l.inlines(h), role, true), width, lead)

	if h.Level == 1 && len(out.lines) > start {
		out.lines = append(out.lines, l.rule(RoleRule, width))
	}
}

func (l *layouter) htmlBlock(out *frag, h *ast.HTMLBlock, width int, text Role, lead int) {
	raw := string(h.Value.Bytes(l.d.src))

	switch h.HTMLBlockKind {
	case ast.HTMLBlockKind2, ast.HTMLBlockKind3, ast.HTMLBlockKind4, ast.HTMLBlockKind5:
		return
	case ast.HTMLBlockKind1:
		if name, _, ok := tagName(strings.TrimLeft(raw, " \t\r\n")); ok && (name == "script" || name == "style") {
			return
		}

		if l.preBlock(out, raw, width, text, lead) {
			return
		}
	}

	l.flow(out, restyle(l.htmlSegs(raw), text, false), width, lead)
}

func (l *layouter) spaces(n int) Line {
	if n <= 0 {
		return nil
	}

	return Line{chromeRun(plainStyle(), strings.Repeat(" ", n))}
}

// hang puts first before the first line appended since start and rest before
// the others, cutting lines at width. A later blank line gets rest only when
// blanks is set.
func hang(out *frag, start int, first, rest Line, width int, blanks bool) {
	for i := start; i < len(out.lines); i++ {
		prefix := rest
		if i == start {
			prefix = first
		}

		if (len(out.lines[i]) == 0 && i > start && !blanks) || len(prefix) == 0 {
			continue
		}

		out.lines[i] = fit(concat(prefix, out.lines[i]), width)
	}
}

// concat joins a and b, merging the runs at the seam when they match.
func concat(a, b Line) Line {
	if len(a) == 0 || len(b) == 0 {
		return append(slices.Clone(a), b...)
	}

	out := make(Line, 0, len(a)+len(b))
	out = append(out, a...)

	if last := &out[len(out)-1]; sameStyle(*last, b[0]) {
		last.Text += b[0].Text
		b = b[1:]
	}

	return append(out, b...)
}

func lineWidth(line Line) int {
	w := 0
	for _, r := range line {
		w += textWidth(r.Text)
	}

	return w
}

// fit cuts line to width cells, ending in … when it had to cut.
func fit(line Line, width int) Line {
	if lineWidth(line) <= width {
		return line
	}

	out := make(Line, 0, len(line))
	left := width - 1

	for _, r := range line {
		w := textWidth(r.Text)
		if w <= left {
			out = append(out, r)
			left -= w

			continue
		}

		if left > 0 {
			if cut := cutCells(r.Text, left); cut != "" {
				r.Text = cut
				out = append(out, r)
			}
		}

		r.Text, r.tab, r.rest = "…", false, ""

		return appendRun(out, r)
	}

	return out
}

// dropUnusedLinks removes the links no run or image refers to, which an empty
// link leaves behind, and renumbers the rest.
func (l *layouter) dropUnusedLinks() {
	if len(l.out.Links) == 0 {
		return
	}

	used := make([]bool, len(l.out.Links))

	for _, line := range l.out.Lines {
		for _, r := range line {
			if r.Link >= 0 {
				used[r.Link] = true
			}
		}
	}

	for _, im := range l.out.Images {
		if im.Link >= 0 {
			used[im.Link] = true
		}
	}

	remap := make([]int, len(used))

	var kept []Link

	for i, u := range used {
		remap[i] = -1

		if u {
			remap[i] = len(kept)
			kept = append(kept, l.out.Links[i])
		}
	}

	for _, line := range l.out.Lines {
		for i := range line {
			if line[i].Link >= 0 {
				line[i].Link = remap[line[i].Link]
			}
		}
	}

	for i := range l.out.Images {
		if l.out.Images[i].Link >= 0 {
			l.out.Images[i].Link = remap[l.out.Images[i].Link]
		}
	}

	l.out.Links = kept
}
