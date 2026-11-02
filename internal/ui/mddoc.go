package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/diffview"
)

const (
	// mdTokenMax bounds the code lines a doc tokenises; later blocks stay plain.
	mdTokenMax = 20000
	// mdTokenWidth is the width of the layout parseMarkdown tokenises through.
	mdTokenWidth = 100
)

// codeTokeniser classes the code lines of one fenced block, as
// diffview.Highlighter.HighlightLang does.
type codeTokeniser func(lang string, lines []string) [][]diffview.Span

// mdSource is what an mdView shows: one document, or several composed into one.
type mdSource interface {
	layout(width int, icons bool) md.Layout
	pictureMove() (md.Layout, bool)
	text() []string
	toggleFold(id int)
	// lineText is what line i of the last layout copies, in the layout's cells.
	lineText(i int) md.LineText
	// gen counts the layouts made, so a holder can tell a reload from a repaint.
	gen() int
}

// mdDoc is a parsed markdown document with its code tokenised, laid out on the
// update loop and cached until the width, the icons or a fold changes.
type mdDoc struct {
	doc   *md.Doc
	spans map[string][][]md.Span // by codeKey; a block past the cap has none
	open  map[int]bool           // by fold ID; a present key overrides the default
	fold  *string                // the key a closed fold names; nil is md's default

	tokenMax int // the code-line cap the doc was parsed with

	lay     md.Layout
	plain   []string // lay's text, made on first use
	have    bool
	width   int
	icons   bool
	layouts int // layouts made

	pics      docPictures
	hasImages bool      // the document has an image, found at parse
	picGen    uint64    // pics.reg's gen at the last layout
	prev      md.Layout // the layout a picture-driven relayout replaced
	hasPrev   bool
}

// codeKey identifies a code block by its language and text.
func codeKey(lang string, lines []string) string {
	return lang + "\x00" + strings.Join(lines, "\n")
}

// parseMarkdown parses src and tokenises its code blocks once, folded ones
// included. It blocks, so it runs only inside a tea.Cmd.
func parseMarkdown(src []byte, base string, tok codeTokeniser) *mdDoc {
	return parseMarkdownCapped(src, base, tok, mdTokenMax)
}

// parseMarkdownCapped is parseMarkdown with the code-line cap a parameter, so
// tests reach the cap with a small document.
func parseMarkdownCapped(src []byte, base string, tok codeTokeniser, tokenMax int) *mdDoc {
	d := &mdDoc{
		tokenMax: tokenMax,
		doc:      md.Parse(src, md.ParseOptions{Base: base}),
		spans:    map[string][][]md.Span{},
		open:     map[int]bool{},
	}

	all := make(map[int]bool, d.doc.FoldCount())
	for i := range d.doc.FoldCount() {
		all[i] = true
	}

	used, capped := 0, false

	opts := md.LayoutOptions{Width: mdTokenWidth, Open: all}
	if tok != nil {
		opts.Highlight = func(lang string, lines []string) [][]md.Span {
			key := codeKey(lang, lines)
			if _, ok := d.spans[key]; ok {
				return nil
			}

			if capped || used+len(lines) > tokenMax {
				capped = true

				return nil
			}

			used += len(lines)

			raw := tok(lang, lines)
			out := make([][]md.Span, len(raw))

			for i, line := range raw {
				for _, s := range line {
					out[i] = append(out[i], md.Span{Start: s.Start, End: s.End, Class: uint8(s.Class)})
				}
			}

			d.spans[key] = out

			return nil
		}
	}

	d.hasImages = len(d.doc.Layout(opts).Images) > 0

	return d
}

// withFoldKey sets the key closed folds name, "" for none, and returns d; the
// key is part of what the cached layout depends on.
func (d *mdDoc) withFoldKey(key *string) *mdDoc {
	d.fold, d.have = key, false

	return d
}

// withPictures lends d the pictures of its view and returns d.
func (d *mdDoc) withPictures(p docPictures) *mdDoc {
	d.pics, d.have = p, false
	d.hasPrev = false

	return d
}

// pictureMove returns the layout a relayout caused by the pictures replaced, once;
// the view maps its position from it. It gives false after a change of width,
// icons or folds, which moves the view by itself.
func (d *mdDoc) pictureMove() (old md.Layout, ok bool) {
	if !d.hasPrev {
		return md.Layout{}, false
	}

	old = d.prev
	d.prev, d.hasPrev = md.Layout{}, false

	return old, true
}

// parseCmd parses off the update loop and answers with done's message.
func parseCmd(src []byte, base string, tok codeTokeniser, done func(*mdDoc) tea.Msg) tea.Cmd {
	return func() tea.Msg { return done(parseMarkdown(src, base, tok)) }
}

// cached reads the spans kept at parse; it never tokenises.
func (d *mdDoc) cached(lang string, lines []string) [][]md.Span {
	return d.spans[codeKey(lang, lines)]
}

// layout is the doc laid out at width, made again only when the width, the
// icons or a fold changed since the last call.
func (d *mdDoc) layout(width int, icons bool) md.Layout {
	reg := d.pics.reg
	same := d.have && d.width == width && d.icons == icons
	moved := reg != nil && d.hasImages && reg.gen() != d.picGen

	if same && !moved {
		return d.lay
	}

	if same {
		if !d.hasPrev {
			d.prev, d.hasPrev = d.lay, true
		}
	} else {
		d.prev, d.hasPrev = md.Layout{}, false
	}

	opts := md.LayoutOptions{Width: width, Open: d.open, FoldKey: d.fold, Icons: icons, Highlight: d.cached}
	if reg != nil {
		opts.Picture = d.pics.size
	}

	d.lay = d.doc.Layout(opts)

	if reg != nil {
		d.picGen = reg.gen()
	}

	d.plain = nil
	d.have, d.width, d.icons = true, width, icons
	d.layouts++

	return d.lay
}

// lineText is what line i of the last layout copies.
func (d *mdDoc) lineText(i int) md.LineText { return d.lay.Text(i) }

// gen is the number of layouts made.
func (d *mdDoc) gen() int { return d.layouts }

// text is the last layout's lines as plain text, for search.
func (d *mdDoc) text() []string {
	if d.plain == nil {
		d.plain = d.lay.Plain()
	}

	return d.plain
}

// toggleFold flips a fold of the last layout; an unknown ID changes nothing.
func (d *mdDoc) toggleFold(id int) {
	for _, f := range d.lay.Folds {
		if f.ID == id {
			cur, ok := d.open[id]
			if !ok {
				cur = f.Open
			}

			d.open[id] = !cur
			d.have = false

			return
		}
	}
}
