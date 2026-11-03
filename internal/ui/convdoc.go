package ui

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git/invisible"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/diffview"
)

// The conversation's own roles, past md's. md counts them as text, so what a
// line copies comes from convDoc.lineText, not from its roles.
const (
	roleConvFrame md.Role = 128 + iota
	roleConvAuthor
	roleConvVerb
	roleConvApproved
	roleConvChanges
	roleConvMeta
	roleConvPath
	roleHunkAddGutter
	roleHunkDelGutter
	roleHunkCtxGutter
	roleHunkAdd
	roleHunkDel
	roleHunkCtx
)

const (
	convHunkLines = 3                // the lines of an inline comment's hunk that show
	convGutterW   = 7                // "NNNN s "
	convTabStop   = 4                // columns a tab in hunk code goes to
	convFoldHint  = "   enter opens" // after a folded bot item
	convEmptyBody = "No description provided."
	convCutRow    = "earlier items not loaded"
	convDescID    = "description"
)

// convBody is one parsed markdown body of the conversation.
type convBody struct {
	doc      *mdDoc
	images   map[string]string // image source as written to the URL that loads it
	foldBase int               // the body's fold IDs start here in the composed layout
}

func (b *convBody) folds() int { return b.doc.doc.FoldCount() }

// convHunkLine is one line of an inline comment's hunk.
type convHunkLine struct {
	role md.Role // the line's gutter role; the code's follows it
	num  int     // 0 when unknown
	sign byte
	segs []cseg
}

// convComment is an inline comment of a review.
type convComment struct {
	loc  string // path:line
	hunk []convHunkLine
	body *convBody
}

// convItem is the description or one timeline item.
type convItem struct {
	id       string
	event    bool   // a row, not a box
	bot      bool   // folds
	botIdx   int    // its place among the bot items
	head     []cseg // a box's header, or an event row's text
	body     *convBody
	empty    bool // the description has no text
	comments []convComment
}

// convRow is one composed line: its runs and what it copies.
type convRow struct {
	runs md.Line
	lt   md.LineText // when body is nil

	// A body line copies what its body's layout says, moved to where the body is
	// drawn on this line.
	body    *md.Layout
	j, col  int
	imgBase int

	item int
}

// convDoc is a pull request's conversation as one mdSource: a box per
// description, comment and review, rows for events, and each body parsed once.
// It is used on the update loop only.
type convDoc struct {
	items   []*convItem
	bodies  []*convBody // in layout order
	cut     bool
	botBase int // the fold ID of the first bot item
	bots    int
	botOpen map[int]bool // by bot index

	pics      docPictures
	hasImages bool

	lay     md.Layout
	lines   []convRow
	starts  []int // the first line of each item
	plain   []string
	have    bool
	width   int
	icons   bool
	gens    int
	picGen  uint64
	prev    md.Layout
	hasPrev bool
}

var _ mdSource = (*convDoc)(nil)

// cseg is a stretch of a line with one role, and what a copy takes from it.
type cseg struct {
	text   string
	raw    string // what a copy takes when it is not text (a tab)
	role   md.Role
	class  uint8
	whole  bool // a copy takes all of it or none
	chrome bool // drawn, never copied
}

func (s cseg) copyText() string {
	if s.raw != "" {
		return s.raw
	}

	return s.text
}

func csegWidth(segs []cseg) int {
	n := 0
	for _, s := range segs {
		n += ansi.StringWidth(s.text)
	}

	return n
}

// fitSegs cuts segs to budget cells, ending in … when anything is left out. The
// … copies the text it stands for.
func fitSegs(segs []cseg, budget int) []cseg {
	if csegWidth(segs) <= budget {
		return segs
	}

	if budget < 1 {
		return nil
	}

	room := budget - 1

	var out []cseg

	for i, s := range segs {
		w := ansi.StringWidth(s.text)
		if w <= room {
			out = append(out, s)
			room -= w

			continue
		}

		var rest strings.Builder

		if !s.whole {
			keep := ansi.Truncate(s.text, room, "")
			if keep != "" {
				k := s
				k.text = keep
				out = append(out, k)
			}

			if !s.chrome {
				rest.WriteString(s.text[len(keep):])
			}
		} else if !s.chrome {
			rest.WriteString(s.copyText())
		}

		for _, t := range segs[i+1:] {
			if !t.chrome {
				rest.WriteString(t.copyText())
			}
		}

		out = append(out, cseg{text: "…", raw: rest.String(), role: s.role, class: s.class, whole: true, chrome: rest.Len() == 0})

		return out
	}

	return out
}

func mkRun(text string, role md.Role) md.Run { return md.Run{Text: text, Role: role, Link: -1} }

// emitSegs is segs as runs and the pieces a copy takes, from cell col.
func emitSegs(segs []cseg, col int) (md.Line, []md.Piece) {
	var (
		runs   md.Line
		pieces []md.Piece
	)

	for _, s := range segs {
		w := ansi.StringWidth(s.text)

		r := mkRun(s.text, s.role)
		r.Class = s.class
		runs = append(runs, r)

		if !s.chrome && s.text != "" {
			pieces = append(pieces, md.Piece{Col: col, Cells: w, Text: s.copyText(), Whole: s.whole})
		}

		col += w
	}

	return runs, pieces
}

// parseConversation parses every body of c and keeps the last lines of each
// inline hunk, highlighted by path. It blocks, so it runs only inside a tea.Cmd.
func parseConversation(c github.Conversation, base string, tok codeTokeniser, hl func(path string, lines []string) [][]diffview.Span, now time.Time) *convDoc {
	d := &convDoc{cut: c.Cut, botOpen: map[int]bool{}}
	folds := 0

	body := func(t github.Text) *convBody {
		if strings.TrimSpace(t.Markdown) == "" {
			return nil
		}

		b := &convBody{doc: parseMarkdown([]byte(t.Markdown), base, tok), images: t.Images, foldBase: folds}
		folds += b.folds()
		d.bodies = append(d.bodies, b)
		d.hasImages = d.hasImages || b.doc.hasImages

		return b
	}

	age := func(at time.Time) string { return railAge(at, now) }

	desc := &convItem{
		id:   convDescID,
		head: convHeader(c.PR.Author, false, "opened #"+strconv.Itoa(c.PR.Number), roleConvVerb, age(c.Created)),
		body: body(c.Body),
	}
	desc.empty = desc.body == nil
	d.items = append(d.items, desc)

	for i, t := range c.Items {
		it := &convItem{id: t.ID}
		if it.id == "" {
			it.id = "item-" + strconv.Itoa(i)
		}

		who := t.Author
		ago := age(t.At)

		switch t.Kind {
		case github.ItemComment:
			it.head = convHeader(who, t.Named, "commented", roleConvVerb, ago)
			it.body = body(t.Body)
		case github.ItemReview:
			verb, role := "reviewed", roleConvVerb

			switch t.State {
			case "APPROVED":
				verb, role = "approved", roleConvApproved
			case "CHANGES_REQUESTED":
				verb, role = "requested changes", roleConvChanges
			}

			it.head = convHeader(who, t.Named, verb, role, ago)
			it.body = body(t.Body)

			for _, rc := range t.Comments {
				it.comments = append(it.comments, convComment{loc: convLoc(rc), hunk: hunkTail(rc, hl), body: body(rc.Body)})
			}
		default:
			it.event = true
			it.head = convEvent(t, ago)
		}

		if !it.event && t.Bot {
			it.bot, it.botIdx = true, d.bots
			d.bots++
		}

		d.items = append(d.items, it)
	}

	d.botBase = folds

	return d
}

// convWho is a login as @login, or a git author name as it is.
func convWho(author string, named bool) string {
	if named {
		return printable(author)
	}

	return "@" + printable(author)
}

func convHeader(author string, named bool, verb string, role md.Role, age string) []cseg {
	segs := []cseg{
		{text: convWho(author, named), role: roleConvAuthor},
		{text: " " + verb, role: role},
	}

	if age != "" {
		segs = append(segs, cseg{text: " · " + age, role: roleConvMeta})
	}

	return segs
}

func convEvent(t github.TimelineItem, age string) []cseg {
	who := convWho(t.Author, t.Named)

	var words string

	switch t.Kind {
	case github.ItemCommits:
		words = who + " pushed " + strconv.Itoa(t.Commits) + " commit"
		if t.Commits != 1 {
			words += "s"
		}
	case github.ItemForcePush:
		words = who + " force-pushed " + printable(t.Ref) + " from " + printable(t.Before) + " to " + printable(t.After)
	case github.ItemReviewRequest:
		words = who + " requested a review from @" + printable(t.Reviewer)
	case github.ItemMerged:
		sha := printable(t.Commit)
		if len(sha) > 7 {
			sha = sha[:7]
		}

		words = who + " merged commit " + sha + " into " + printable(t.Base)
	case github.ItemClosed:
		words = who + " closed this"
	default:
		words = who
	}

	if age != "" {
		words += " · " + age
	}

	return []cseg{{text: words, role: roleConvMeta}}
}

func convLoc(rc github.ReviewComment) string {
	loc := printable(rc.Path)
	if loc != "" && rc.Line > 0 {
		loc += ":" + strconv.Itoa(rc.Line)
	}

	return loc
}

var hunkHead = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// hunkTail is the last convHunkLines lines of rc's hunk, numbered and highlighted.
func hunkTail(rc github.ReviewComment, hl func(string, []string) [][]diffview.Span) []convHunkLine {
	if rc.Hunk == "" {
		return nil
	}

	type row struct {
		sign byte
		num  int
		code string
	}

	var rows []row

	oldN, newN := 0, 0

	for l := range strings.SplitSeq(strings.TrimSuffix(rc.Hunk, "\n"), "\n") {
		if m := hunkHead.FindStringSubmatch(l); m != nil {
			oldN, _ = strconv.Atoi(m[1])
			newN, _ = strconv.Atoi(m[2])

			continue
		}

		l = strings.TrimSuffix(l, "\r")

		switch {
		case strings.HasPrefix(l, "\\"):
		case strings.HasPrefix(l, "+"):
			rows = append(rows, row{'+', newN, l[1:]})
			newN++
		case strings.HasPrefix(l, "-"):
			rows = append(rows, row{'-', oldN, l[1:]})
			oldN++
		default:
			code := l
			if strings.HasPrefix(l, " ") {
				code = l[1:]
			}

			rows = append(rows, row{' ', newN, code})
			oldN++
			newN++
		}
	}

	rows = rows[max(len(rows)-convHunkLines, 0):]

	var spans [][]diffview.Span

	if hl != nil && len(rows) > 0 {
		codes := make([]string, len(rows))
		for i, r := range rows {
			codes[i] = r.code
		}

		spans = hl(rc.Path, codes)
	}

	out := make([]convHunkLine, len(rows))

	for i, r := range rows {
		var sp []diffview.Span
		if i < len(spans) {
			sp = spans[i]
		}

		gutter, code := roleHunkCtxGutter, roleHunkCtx

		switch r.sign {
		case '+':
			gutter, code = roleHunkAddGutter, roleHunkAdd
		case '-':
			gutter, code = roleHunkDelGutter, roleHunkDel
		}

		out[i] = convHunkLine{role: gutter, num: max(r.num, 0), sign: r.sign, segs: codeSegs(r.code, sp, code)}
	}

	return out
}

// codeSegs is a code line as segs in role: tabs go to convTabStop columns and a
// character that draws nothing or controls the terminal becomes a ⟨U+XXXX⟩ tag.
func codeSegs(code string, spans []diffview.Span, role md.Role) []cseg {
	var (
		out   []cseg
		plain strings.Builder
		class uint8
		col   int
	)

	flush := func() {
		if plain.Len() > 0 {
			out = append(out, cseg{text: plain.String(), role: role, class: class})
			plain.Reset()
		}
	}

	classAt := func(i int) uint8 {
		for _, s := range spans {
			if s.Start <= i && i < s.End {
				return uint8(s.Class)
			}
		}

		return 0
	}

	for i := 0; i < len(code); {
		r, size := utf8.DecodeRuneInString(code[i:])

		c := classAt(i)
		i += size

		if c != class {
			flush()

			class = c
		}

		switch {
		case r == '\t':
			flush()

			n := convTabStop - col%convTabStop
			out = append(out, cseg{text: strings.Repeat(" ", n), raw: "\t", role: role, class: class, whole: true})
			col += n
		case r == utf8.RuneError && size == 1:
			plain.WriteRune('�')

			col++
		case unicode.IsControl(r) || invisible.Rune(r):
			flush()

			tag := fmt.Sprintf("⟨U+%04X⟩", r)
			out = append(out, cseg{text: tag, role: md.RoleHidden, whole: true})
			col += ansi.StringWidth(tag)
		default:
			plain.WriteRune(r)

			col += ansi.StringWidth(string(r))
		}
	}

	flush()

	return out
}

// withPictures lends every body the pictures of its view, with the signed URLs
// of its own images, and returns d.
func (d *convDoc) withPictures(p docPictures) *convDoc {
	d.pics, d.have, d.hasPrev = p, false, false

	for _, b := range d.bodies {
		q := p
		q.alias = b.images
		b.doc.withPictures(q)
	}

	return d
}

// carryOpen opens in d what the user opened in old, by item id: bot boxes and
// the folds of each body whose fold count is unchanged.
func (d *convDoc) carryOpen(old *convDoc) {
	if old == nil {
		return
	}

	byID := make(map[string]*convItem, len(old.items))
	for _, it := range old.items {
		byID[it.id] = it
	}

	body := func(n, o *convBody) {
		if n != nil && o != nil && n.folds() == o.folds() {
			maps.Copy(n.doc.open, o.doc.open)
			n.doc.have = false
		}
	}

	for _, it := range d.items {
		o := byID[it.id]
		if o == nil {
			continue
		}

		if it.bot && o.bot && old.botOpen[o.botIdx] {
			d.botOpen[it.botIdx] = true
		}

		body(it.body, o.body)

		if len(it.comments) == len(o.comments) {
			for i := range it.comments {
				body(it.comments[i].body, o.comments[i].body)
			}
		}
	}

	d.have = false
}

// pictureMove returns the layout a relayout caused by the pictures replaced, once.
func (d *convDoc) pictureMove() (md.Layout, bool) {
	if !d.hasPrev {
		return md.Layout{}, false
	}

	old := d.prev
	d.prev, d.hasPrev = md.Layout{}, false

	return old, true
}

// boxFold is the fold ID a box passes to the composer: a bot's own, else 0.
func (d *convDoc) boxFold(it *convItem) int {
	if it.bot {
		return d.botBase + it.botIdx
	}

	return 0
}

// gen is the number of layouts made. A fold toggled since counts at the next
// layout, not at the toggle.
func (d *convDoc) gen() int { return d.gens }

// text is the last layout's lines as plain text, for search.
func (d *convDoc) text() []string {
	if d.plain == nil {
		d.plain = d.lay.Plain()
	}

	return d.plain
}

// toggleFold flips the fold with the composed ID id: a body's, or a bot item's.
func (d *convDoc) toggleFold(id int) {
	for _, b := range d.bodies {
		if id >= b.foldBase && id < b.foldBase+b.folds() {
			b.doc.toggleFold(id - b.foldBase)

			d.have = false

			return
		}
	}

	if k := id - d.botBase; k >= 0 && k < d.bots {
		d.botOpen[k] = !d.botOpen[k]
		d.have = false
	}
}

// layout is the conversation laid out at width, made again only when the width,
// the icons, a fold or the pictures changed since the last call.
func (d *convDoc) layout(width int, icons bool) md.Layout {
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

	d.compose(max(width, 1), icons)

	if reg != nil {
		d.picGen = reg.gen()
	}

	d.plain = nil
	d.have, d.width, d.icons = true, width, icons
	d.gens++

	return d.lay
}

// composer is a layout being made.
type composer struct {
	w, inner int
	icons    bool
	lines    []convRow
	links    []md.Link
	images   []md.Image
	folds    []md.Fold
	item     int
}

func (c *composer) add(l convRow) {
	l.item = c.item
	c.lines = append(c.lines, l)
}

func (d *convDoc) compose(w int, icons bool) {
	c := &composer{w: w, inner: max(w-4, 0), icons: icons}

	d.starts = d.starts[:0]

	for i, it := range d.items {
		c.item = i
		d.starts = append(d.starts, len(c.lines))

		switch {
		case it.event:
			c.row(it.head)
		case it.bot && !d.botOpen[it.botIdx]:
			c.botRow(it, d.botBase+it.botIdx)
		default:
			c.box(it, d.boxFold(it))
		}

		if i == 0 && d.cut {
			c.row([]cseg{{text: convCutRow, role: roleConvMeta}})
		}
	}

	d.lines = c.lines
	d.lay = md.Layout{Lines: make([]md.Line, len(c.lines)), Source: make([]md.SourceRange, len(c.lines)), Links: c.links, Images: c.images, Folds: c.folds}

	for i, l := range c.lines {
		d.lay.Lines[i] = l.runs
	}
}

// row is an event-like row: two cells in, cut and padded to the width.
func (c *composer) row(segs []cseg) {
	segs = fitSegs(segs, max(c.w-2, 0))
	lead := []cseg{{text: "  ", role: md.RoleText, chrome: true}}

	all := slices.Concat(lead, segs)
	if pad := c.w - csegWidth(all); pad > 0 {
		all = append(all, cseg{text: strings.Repeat(" ", pad), role: md.RoleText, chrome: true})
	}

	runs, pieces := emitSegs(all, 0)
	c.add(convRow{runs: runs, lt: md.LineText{Pieces: pieces}})
}

// botRow is a bot item folded: ▸, its header and the hint.
func (c *composer) botRow(it *convItem, id int) {
	c.folds = append(c.folds, md.Fold{ID: id, Line: len(c.lines)})

	head := fitSegs(it.head, max(c.w-2-2-len(convFoldHint), 1))
	segs := []cseg{
		{text: "  ", role: md.RoleText, chrome: true},
		{text: "▸ ", role: md.RoleSummaryMark, chrome: true},
	}
	segs = append(segs, head...)
	segs = append(segs, cseg{text: convFoldHint, role: md.RoleFoldHint, chrome: true})

	if pad := c.w - csegWidth(segs); pad > 0 {
		segs = append(segs, cseg{text: strings.Repeat(" ", pad), role: md.RoleText, chrome: true})
	}

	runs, pieces := emitSegs(segs, 0)
	c.add(convRow{runs: runs, lt: md.LineText{Pieces: pieces}})
}

// box is a bordered item: header, body, inline comments, bottom edge.
func (c *composer) box(it *convItem, foldID int) {
	head := it.head

	if it.bot {
		c.folds = append(c.folds, md.Fold{ID: foldID, Line: len(c.lines), Open: true})
		head = append([]cseg{{text: "▾ ", role: md.RoleSummaryMark, chrome: true}}, head...)
	}

	head = fitSegs(head, max(c.w-5, 0))
	runs := md.Line{mkRun("╭─ ", roleConvFrame)}
	hr, pieces := emitSegs(head, 3)
	runs = append(runs, hr...)
	runs = append(runs, mkRun(" "+strings.Repeat("─", max(c.w-5-csegWidth(head), 0))+"╮", roleConvFrame))
	c.add(convRow{runs: runs, lt: md.LineText{Pieces: pieces}})

	switch {
	case it.empty:
		c.boxRow([]cseg{{text: convEmptyBody, role: roleConvMeta}})
	case it.body != nil:
		c.body(it.body, 0)
	}

	for _, rc := range it.comments {
		c.boxRow(nil)

		if rc.loc != "" {
			c.boxRow([]cseg{{text: "  ", role: md.RoleText, chrome: true}, {text: ansiFit(rc.loc, c.inner-2), role: roleConvPath}})
		}

		for _, h := range rc.hunk {
			c.hunkRow(h)
		}

		if rc.body != nil {
			c.body(rc.body, 2)
		}
	}

	c.add(convRow{
		runs: md.Line{mkRun("╰"+strings.Repeat("─", max(c.w-2, 0))+"╯", roleConvFrame)},
		lt:   md.LineText{Skip: true},
	})
}

// ansiFit cuts s to w cells with a trailing ….
func ansiFit(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}

	if w < 1 {
		return ""
	}

	return ansi.Truncate(s, w-1, "") + "…"
}

// boxRow is segs between the box's sides, padded to its inner width.
func (c *composer) boxRow(segs []cseg) {
	segs = fitSegs(segs, c.inner)
	if pad := c.inner - csegWidth(segs); pad > 0 {
		segs = append(segs, cseg{text: strings.Repeat(" ", pad), role: md.RoleText, chrome: true})
	}

	runs := md.Line{mkRun("│ ", roleConvFrame)}
	rs, pieces := emitSegs(segs, 2)
	runs = append(runs, rs...)
	runs = append(runs, mkRun(" │", roleConvFrame))
	c.add(convRow{runs: runs, lt: md.LineText{Pieces: pieces}})
}

// hunkRow is one hunk line: two cells in, the gutter, the code, and the tint to
// the inner edge.
func (c *composer) hunkRow(h convHunkLine) {
	code := roleHunkCtx

	switch h.role {
	case roleHunkAddGutter:
		code = roleHunkAdd
	case roleHunkDelGutter:
		code = roleHunkDel
	}

	num := "    "
	if h.num > 0 {
		num = fmt.Sprintf("%4d", h.num)
	}

	sign := string(h.sign)

	segs := []cseg{
		{text: "  ", role: md.RoleText, chrome: true},
		{text: num + " " + sign + " ", role: h.role, chrome: true},
	}
	segs = append(segs, fitSegs(h.segs, max(c.inner-2-convGutterW, 0))...)

	if pad := c.inner - csegWidth(segs); pad > 0 {
		segs = append(segs, cseg{text: strings.Repeat(" ", pad), role: code, chrome: true})
	}

	c.boxRow(segs)
}

// body adds a body's lines inside the box, indent cells in, and shifts its links,
// images and folds into the composed layout.
func (c *composer) body(b *convBody, indent int) {
	lay := b.doc.layout(max(c.inner-indent, 1), c.icons)
	b.doc.pictureMove()

	ref := &lay
	off, linkBase, imgBase := len(c.lines), len(c.links), len(c.images)

	for j, line := range lay.Lines {
		runs := make(md.Line, 0, len(line)+4)
		runs = append(runs, mkRun("│ ", roleConvFrame))

		if indent > 0 {
			runs = append(runs, mkRun(strings.Repeat(" ", indent), md.RoleText))
		}

		got := 0

		for _, r := range line {
			if r.Link >= 0 {
				r.Link += linkBase
			}

			got += ansi.StringWidth(r.Text)
			runs = append(runs, r)
		}

		if pad := c.inner - indent - got; pad > 0 {
			runs = append(runs, mkRun(strings.Repeat(" ", pad), md.RoleText))
		}

		runs = append(runs, mkRun(" │", roleConvFrame))
		c.add(convRow{runs: runs, body: ref, j: j, col: 2 + indent, imgBase: imgBase})
	}

	c.links = append(c.links, lay.Links...)

	for _, im := range lay.Images {
		im.Line += off
		if im.Link >= 0 {
			im.Link += linkBase
		}

		c.images = append(c.images, im)
	}

	for _, f := range lay.Folds {
		c.folds = append(c.folds, md.Fold{ID: f.ID + b.foldBase, Line: f.Line + off, Open: f.Open})
	}
}

// lineText is what line i of the last layout copies.
func (d *convDoc) lineText(i int) md.LineText {
	if i < 0 || i >= len(d.lines) {
		return md.LineText{}
	}

	l := d.lines[i]
	if l.body == nil {
		return l.lt
	}

	lt := l.body.Text(l.j)
	lt.Pieces = slices.Clone(lt.Pieces)

	for k := range lt.Pieces {
		lt.Pieces[k].Col += l.col

		if lt.Pieces[k].Once > 0 {
			lt.Pieces[k].Once += l.imgBase
		}
	}

	return lt
}

// itemIndex is the item that has line i, or -1.
func (d *convDoc) itemIndex(i int) int {
	if i < 0 || i >= len(d.lines) {
		return -1
	}

	return sort.Search(len(d.starts), func(k int) bool { return d.starts[k] > i }) - 1
}

// itemLine is the first line of the item after line from (dir > 0), or the
// nearest item first line above it (dir < 0), which is the current item's own
// top from inside it; false when there is none.
func (d *convDoc) itemLine(from, dir int) (int, bool) {
	k := d.itemIndex(from)
	if k < 0 {
		return 0, false
	}

	if dir > 0 {
		k++
	} else if d.starts[k] == from {
		k--
	}

	if k < 0 || k >= len(d.starts) {
		return 0, false
	}

	return d.starts[k], true
}

// itemAt is the id of the item holding line i and the line's offset in it, to
// find the place again after a read.
func (d *convDoc) itemAt(line int) (string, int) {
	k := d.itemIndex(line)
	if k < 0 {
		return "", 0
	}

	return d.items[k].id, line - d.starts[k]
}

// lineOf is the line at offset in the item id, kept inside the item; -1 when no
// item has that id.
func (d *convDoc) lineOf(id string, offset int) int {
	k := slices.IndexFunc(d.items, func(it *convItem) bool { return it.id == id })
	if k < 0 || k >= len(d.starts) {
		return -1
	}

	end := len(d.lines)
	if k+1 < len(d.starts) {
		end = d.starts[k+1]
	}

	return min(d.starts[k]+max(offset, 0), max(end-1, d.starts[k]))
}
