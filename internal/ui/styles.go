package ui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// styles are the Lip Gloss styles the shell draws with, all coloured from one
// theme.
type styles struct {
	body       lipgloss.Style
	card       lipgloss.Style
	cardTitle  lipgloss.Style
	cardMuted  lipgloss.Style
	cardFiller lipgloss.Style
	cardKey    lipgloss.Style
	popup      lipgloss.Style
	popupKey   lipgloss.Style
	// popupHead is the accent bold title and group head; popupRule draws the divider lines.
	popupHead, popupRule lipgloss.Style
	// border draws unfocused frame lines; borderAgent and borderShell the
	// focused pane's.
	border, borderAgent, borderShell lipgloss.Style
	// kindAgent and kindShell are bold in the kind colour: icon and focused title.
	kindAgent, kindShell lipgloss.Style
	titleMuted, tagZoom  lipgloss.Style
	// tagScroll, tagNext and tagHidden are the scroll, preselection and hidden-column tags.
	tagScroll, tagNext, tagHidden lipgloss.Style
	// tagCopy is the copy tag and the search prompt's mark; searchText is the query.
	tagCopy, searchText lipgloss.Style
	// tagModel is the model tag's model name and tagMeter its context meter, by level.
	tagModel lipgloss.Style
	tagMeter [meterLevels]lipgloss.Style
	// The toast: its border and icon in the level's colour, and its text, all on the panel colour.
	toastTone [toastLevels]lipgloss.Style
	toastText lipgloss.Style
	// tagWorking, tagBlocked and tagIdle draw the state badge.
	tagWorking, tagBlocked, tagIdle lipgloss.Style
	// popupBorder draws the popup frame on the panel colour.
	popupBorder lipgloss.Style
	// popupMuted and popupSubtle are secondary text on the panel colour.
	popupMuted, popupSubtle lipgloss.Style
	// The confirm box: its Error border and title, bold question and bold keys.
	confirmBorder, confirmTitle, confirmQuestion, confirmKey lipgloss.Style
	// pushTitle is the push confirm's title, in Info and bold.
	pushTitle lipgloss.Style
	// approveTitle is the approve and ready confirms' title, in Success and bold.
	approveTitle lipgloss.Style
	// mergeTitle is the merge box's title, in Accent and bold.
	mergeTitle lipgloss.Style
	// popupAccent, popupDim and popupWarn are checked boxes, paths and warnings on the panel colour.
	popupAccent, popupDim, popupWarn lipgloss.Style
	// runs memoizes rendered border runs; it is shared by copies of the styles.
	runs *runCache
}

func newStyles(t theme.Theme) styles {
	ui := t.UI
	onBg := lipgloss.NewStyle().Background(ui.Bg).Foreground(ui.Fg)
	onPanel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)

	var meters [meterLevels]lipgloss.Style

	for l := range meterLevels {
		fg, bold := l.colour(ui)
		meters[l] = onBg.Foreground(fg).Bold(bold)
	}

	var toasts [toastLevels]lipgloss.Style

	for l := range toastLevels {
		toasts[l] = onPanel.Foreground(l.colour(ui))
	}

	return styles{
		body:        onBg,
		card:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ui.Border).BorderBackground(ui.Bg),
		cardTitle:   onPanel.Foreground(ui.Accent).Bold(true),
		cardMuted:   onPanel.Foreground(ui.Muted),
		cardFiller:  onPanel,
		cardKey:     onPanel.Foreground(ui.Key),
		popup:       onPanel,
		popupKey:    onPanel.Foreground(ui.Key).Bold(true),
		popupHead:   onPanel.Foreground(ui.Accent).Bold(true),
		popupRule:   onPanel.Foreground(ui.Border),
		border:      onBg.Foreground(ui.Border),
		borderAgent: onBg.Foreground(ui.Agent),
		borderShell: onBg.Foreground(ui.Shell),
		kindAgent:   onBg.Foreground(ui.Agent).Bold(true),
		kindShell:   onBg.Foreground(ui.Shell).Bold(true),
		titleMuted:  onBg.Foreground(ui.Muted),
		tagZoom:     onBg.Foreground(ui.Accent).Bold(true),
		tagScroll:   onBg.Foreground(ui.Accent).Bold(true),
		tagNext:     onBg.Foreground(ui.Accent).Bold(true),
		tagHidden:   onBg.Foreground(ui.Accent).Bold(true),
		tagCopy:     onBg.Foreground(ui.Info).Bold(true),
		searchText:  onBg,
		tagModel:    onBg.Foreground(ui.Muted),
		tagMeter:    meters,
		toastTone:   toasts,
		toastText:   onPanel,
		tagWorking:  onBg.Foreground(ui.Working).Bold(true),
		tagBlocked:  onBg.Foreground(ui.Blocked).Bold(true),
		tagIdle:     onBg.Foreground(ui.Idle).Bold(true),
		popupBorder: lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.BorderFocus),
		popupMuted:  onPanel.Foreground(ui.Muted),
		popupSubtle: onPanel.Foreground(ui.Subtle),

		confirmBorder:   onPanel.Foreground(ui.Error),
		confirmTitle:    onPanel.Foreground(ui.Error).Bold(true),
		confirmQuestion: onPanel.Bold(true),
		confirmKey:      onPanel.Foreground(ui.Key).Bold(true),
		pushTitle:       onPanel.Foreground(ui.Info).Bold(true),
		approveTitle:    onPanel.Foreground(ui.Success).Bold(true),
		mergeTitle:      onPanel.Foreground(ui.Accent).Bold(true),
		popupAccent:     onPanel.Foreground(ui.Accent),
		popupDim:        onPanel.Foreground(ui.Dim),
		popupWarn:       onPanel.Foreground(ui.Warning),
		runs:            &runCache{},
	}
}

// runKey identifies a rendered border run: the style owner and its glyphs.
type runKey struct {
	own    uint8
	glyphs string
}

// runCache holds rendered border runs, which repeat on every frame. It is only
// used on the update loop and is dropped whole when it passes runCacheMax.
type runCache struct {
	m map[runKey]string
}

const runCacheMax = 1024

func (s styles) borderRun(own uint8, glyphs string) string {
	k := runKey{own, glyphs}
	if r, ok := s.runs.m[k]; ok {
		return r
	}

	if s.runs.m == nil || len(s.runs.m) >= runCacheMax {
		s.runs.m = make(map[runKey]string)
	}

	r := s.borderStyle(own).Render(glyphs)
	s.runs.m[k] = r

	return r
}

func (s styles) borderStyle(own uint8) lipgloss.Style {
	switch own {
	case ownAgent:
		return s.borderAgent
	case ownShell:
		return s.borderShell
	}

	return s.border
}

// badge is the state badge's text and style; ok is false for a pane with no
// state to show.
func (s styles) badge(st core.AgentState, spin int, icons bool) (text string, style lipgloss.Style, ok bool) {
	switch st {
	case core.AgentWorking:
		return " " + spinFrame(spin) + " working ", s.tagWorking, true
	case core.AgentBlocked:
		if icons {
			return " " + iconBlocked + " blocked ", s.tagBlocked, true
		}

		return " blocked ", s.tagBlocked, true
	case core.AgentIdle:
		if icons {
			return " " + iconIdle + " idle ", s.tagIdle, true
		}

		return " idle ", s.tagIdle, true
	}

	return "", lipgloss.Style{}, false
}

func (s styles) kind(k core.PaneKind) lipgloss.Style {
	if k == core.KindAgent {
		return s.kindAgent
	}

	return s.kindShell
}

// meterLevel is how urgent a context percentage reads, and so its colour.
type meterLevel uint8

const (
	meterCalm meterLevel = iota // below 70
	meterWarm                   // from 70
	meterHot                    // from 90
	meterLevels
)

// meterOf is a context percentage as drawn: clamped to 0-100, as text and level.
func meterOf(pct int) (text string, level meterLevel) {
	pct = min(max(pct, 0), 100)

	switch {
	case pct >= 90:
		level = meterHot
	case pct >= 70:
		level = meterWarm
	}

	return strconv.Itoa(pct) + "%", level
}

// colour is the level's foreground; the hottest level is also bold.
func (l meterLevel) colour(ui theme.UI) (fg theme.Hex, bold bool) {
	switch l {
	case meterHot:
		return ui.Error, true
	case meterWarm:
		return ui.Warning, false
	}

	return ui.Meter, false
}

// cleanModel is a model's display name as the status line sent it, without
// control and invisible characters; empty when nothing is left.
func cleanModel(name string) string {
	return strings.TrimSpace(printable(name))
}

// modelTag is the frame's tag: the model, then the meter when known. text is
// the tag's plain text, for its width, and rendered the styled text.
func (s styles) modelTag(model string, pct int, known bool) (text, rendered string) {
	text = " " + model + " "
	rendered = s.body.Render(" ") + s.tagModel.Render(model) + s.body.Render(" ")

	if known {
		meter, level := meterOf(pct)
		text += meter + " "
		rendered += s.tagMeter[level].Render(meter) + s.body.Render(" ")
	}

	return text, rendered
}
