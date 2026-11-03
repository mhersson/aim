package md

import (
	"html"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/v2/ast"

	"github.com/mhersson/aim/internal/git/invisible"
)

const (
	tabStop     = 4
	maxLangCell = 20
)

// codeBlock lays out a fenced or indented code block as a rounded box. Lines
// are cut with …, never wrapped. The box is built at least numW+6 cells wide
// (room for one code cell) and then cut to width, so no line is ever wider.
func (l *layouter) codeBlock(out *frag, n *ast.CodeBlock, width int) {
	lang := n.Info.Value(l.d.src)
	if i := strings.IndexAny(lang, " \t"); i >= 0 {
		lang = lang[:i]
	}

	l.codeBox(out, codeLines(n, l.d.src), lang, width)
}

// codeBox lays out lines as a code block labelled lang.
func (l *layouter) codeBox(out *frag, lines []string, lang string, width int) {
	numW := 0
	if len(lines) > 0 {
		numW = len(strconv.Itoa(len(lines)))
	}

	fw := max(width, numW+6)
	frame := styled(RoleCodeFrame)

	var spans [][]Span
	if len(lines) > 0 && l.highlight != nil {
		spans = l.highlight(lang, lines)
	}

	label := cleanProse(lang)
	if maxL := min(maxLangCell, fw-6); label == "" || maxL < 1 {
		label = ""
	} else if textWidth(label) > maxL {
		label = cutCells(label, maxL-1) + "…"
	}

	var top Line

	if label == "" {
		top = Line{frame.run("╭" + strings.Repeat("─", fw-2) + "╮")}
	} else {
		fill := strings.Repeat("─", fw-5-textWidth(label))
		top = Line{frame.run("╭─ "), styled(RoleCodeLang).run(label), frame.run(" " + fill + "╮")}
	}

	out.lines = append(out.lines, fit(top, width))

	room := fw - 5 - numW

	for i, text := range lines {
		var sp []Span
		if i < len(spans) {
			sp = spans[i]
		}

		num := strconv.Itoa(i + 1)
		row := Line{frame.run("│ "), styled(RoleLineNumber).run(strings.Repeat(" ", numW-len(num)) + num + " ")}
		row = append(row, codeCells(text, sp, room)...)
		row = appendRun(row, frame.run(" │"))

		out.lines = append(out.lines, fit(row, width))
	}

	out.lines = append(out.lines, fit(Line{frame.run("╰" + strings.Repeat("─", fw-2) + "╯")}, width))
}

// preBlock lays out an HTML block that opens with <pre> as a code block with
// no language; text after the closing tag flows as HTML. It reports false when
// the block does not open with <pre>.
func (l *layouter) preBlock(out *frag, raw string, width int, text Role, lead int) bool {
	raw = strings.TrimLeft(raw, " \t\r\n")
	if name, closing, ok := tagName(raw); !ok || closing || name != "pre" {
		return false
	}

	body, rest := splitPre(raw[tagEnd(raw, 0):])
	body = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(plainHTML(body), "\r"), "\n"), "\n")

	var lines []string

	if body != "" {
		for line := range strings.SplitSeq(body, "\n") {
			lines = append(lines, strings.TrimSuffix(line, "\r"))
		}
	}

	l.codeBox(out, lines, "", max(width-lead, 1))

	if strings.TrimSpace(rest) != "" {
		l.flow(out, restyle(l.htmlSegs(rest), text, false), width, lead)
	}

	return true
}

// splitPre cuts s at its closing </pre> tag; without one all of s is the body.
func splitPre(s string) (body, rest string) {
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			break
		}

		i += j
		if name, closing, ok := tagName(s[i:]); ok && closing && name == "pre" {
			return s[:i], s[tagEnd(s, i):]
		}

		i++
	}

	return s, ""
}

// plainHTML is s with its tags and comments dropped and its entities decoded;
// the whitespace of the text is kept.
func plainHTML(s string) string {
	var b strings.Builder

	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			b.WriteString(html.UnescapeString(s[i:]))

			break
		}

		b.WriteString(html.UnescapeString(s[i : i+j]))
		i += j

		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				break
			}

			i += 4 + end + 3

			continue
		}

		if _, _, ok := tagName(s[i:]); ok {
			i = tagEnd(s, i)

			continue
		}

		b.WriteByte('<')

		i++
	}

	return b.String()
}

// codeLines is the block's lines as written: goldmark's padding applied, the
// line ending dropped.
func codeLines(n *ast.CodeBlock, src []byte) []string {
	segs := n.Value.Segments()
	lines := make([]string, 0, len(segs))

	for _, sg := range segs {
		s := string(sg.Bytes(src))
		s = strings.TrimSuffix(s, "\n")
		s = strings.TrimSuffix(s, "\r")

		lines = append(lines, s)
	}

	return lines
}

// spanClasses spreads spans over the bytes of line: clamped to the line, a
// later span overriding an earlier one.
func spanClasses(line string, spans []Span) []uint8 {
	if len(spans) == 0 {
		return nil
	}

	classes := make([]uint8, len(line))

	for _, sp := range spans {
		start, end := max(sp.Start, 0), min(sp.End, len(line))
		for i := start; i < end; i++ {
			classes[i] = sp.Class
		}
	}

	return classes
}

// codeCell is one unit of code laid out: its run and the cells it takes.
type codeCell struct {
	run   Run
	cells int
	at    int // byte offset in the line
}

// codeCells lays line out in exactly room cells: tabs expanded, hidden
// characters tagged, padded with spaces, or cut to room-1 cells and a …. It
// walks grapheme clusters and stops reading the line once room is full.
func codeCells(line string, spans []Span, room int) Line {
	classes := spanClasses(line, spans)
	cells := make([]codeCell, 0, min(len(line), room+1))
	col, cut, cutAt := 0, false, 0

	// emit adds one unit starting at byte offset at; it reports false when the
	// unit does not fit.
	emit := func(at int, text string, cw int, hidden, tab bool) bool {
		if col+cw > room {
			cutAt = at

			return false
		}

		st := styled(RoleCodeText)
		if hidden {
			st.role, st.bold = RoleHidden, true
		} else if classes != nil {
			st.class = classes[at]
		}

		run := st.run(text)
		run.tab = tab

		cells = append(cells, codeCell{run: run, cells: cw, at: at})
		col += cw

		return true
	}

	for i := 0; i < len(line) && !cut; {
		g, gw := nextCluster(line[i:])
		at := i
		i += len(g)

		if len(g) == 1 && g[0] >= 0x20 && g[0] < 0x7f {
			cut = !emit(at, g, gw, false, false)

			continue
		}

		// Split the cluster around each tagged, expanded or invalid character
		// and measure the plain pieces on their own; a cluster with none is one unit.
		start := 0

		for p := 0; p < len(g) && !cut; {
			r, size := utf8.DecodeRuneInString(g[p:])

			var (
				text   string
				hidden bool
				tab    = r == '\t'
			)

			switch {
			case r == utf8.RuneError && size == 1:
				text = "�"
			case tab:
			case unicode.IsControl(r) || invisible.Rune(r):
				text, hidden = hiddenTag(r), true
			default:
				p += size

				continue
			}

			if p > start {
				cut = !emit(at+start, g[start:p], textWidth(g[start:p]), false, false)
			}

			if tab {
				text = strings.Repeat(" ", tabStop-col%tabStop)
			}

			if !cut {
				cut = !emit(at+p, text, textWidth(text), hidden, tab)
			}

			p += size
			start = p
		}

		if !cut && start < len(g) {
			w := gw
			if start > 0 {
				w = textWidth(g[start:])
			}

			cut = !emit(at+start, g[start:], w, false, false)
		}
	}

	if cut {
		for col > room-1 {
			col -= cells[len(cells)-1].cells
			cutAt = cells[len(cells)-1].at
			cells = cells[:len(cells)-1]
		}
	}

	out := make(Line, 0, 4)
	for _, c := range cells {
		out = appendRun(out, c.run)
	}

	pad := styled(RoleCodeText)

	if cut {
		ellipsis := pad.run("…")
		ellipsis.rest = codeRest(line[cutAt:])

		out = appendRun(out, chromeRun(pad, strings.Repeat(" ", room-1-col)))
		out = appendRun(out, ellipsis)
	} else {
		out = appendRun(out, chromeRun(pad, strings.Repeat(" ", room-col)))
	}

	return out
}

// codeRest is what a copy gives for the part of a code line a cut dropped: tabs
// stay tabs, hidden characters become their tags.
func codeRest(s string) string {
	plain := true

	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 && c != '\t') || c >= 0x7f {
			plain = false

			break
		}
	}

	if plain {
		return s
	}

	var b strings.Builder

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size

		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune('�')
		case r == '\t':
			b.WriteByte('\t')
		case unicode.IsControl(r) || invisible.Rune(r):
			b.WriteString(hiddenTag(r))
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}

// appendRun adds r to line, merging it into the last run when the styles match
// and skipping empty text.
func appendRun(line Line, r Run) Line {
	if r.Text == "" {
		return line
	}

	if n := len(line); n > 0 && sameStyle(line[n-1], r) {
		line[n-1].Text += r.Text

		return line
	}

	return append(line, r)
}

// appendCode adds code text as segs in st: characters that draw nothing or
// control the terminal become a RoleHidden tag, so they stay visible. A tab
// becomes a space.
func appendCode(segs []seg, s string, st style) []seg {
	hidden := st
	hidden.role, hidden.bold = RoleHidden, true

	var plain strings.Builder

	flush := func() {
		if plain.Len() > 0 {
			segs = append(segs, seg{text: plain.String(), st: st, sp: spaceKeep})
			plain.Reset()
		}
	}

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size

		switch {
		case r == utf8.RuneError && size == 1:
			plain.WriteRune('�')
		case r == '\t':
			plain.WriteByte(' ')
		case unicode.IsControl(r) || invisible.Rune(r):
			flush()

			segs = append(segs, seg{text: hiddenTag(r), st: hidden})
		default:
			plain.WriteRune(r)
		}
	}

	flush()

	return segs
}

// hiddenTag spells r as ⟨U+XXXX⟩.
func hiddenTag(r rune) string {
	h := strings.ToUpper(strconv.FormatInt(int64(r), 16))
	if len(h) < 4 {
		h = strings.Repeat("0", 4-len(h)) + h
	}

	return "⟨U+" + h + "⟩"
}
