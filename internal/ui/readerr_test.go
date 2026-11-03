package ui

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/github"
)

var cutWord = regexp.MustCompile(`[A-Za-z0-9.,]…`)

const longReason = "HTTP 502 Bad Gateway from the upstream proxy after three retries, the backend is unavailable so please wait a moment and try again zzEND"

// wholeReason fails the test unless every word of longReason is on screen and
// row of it was cut with an ellipsis.
func wholeReason(t *testing.T, text string) {
	t.Helper()

	for w := range strings.FieldsSeq(longReason) {
		if !strings.Contains(text, w) {
			t.Errorf("the word %q of the reason is not shown:\n%s", w, text)

			return
		}
	}

	for l := range strings.SplitSeq(text, "\n") {
		if cutWord.MatchString(l) && (strings.Contains(l, "upstream") || strings.Contains(l, "backend") || strings.Contains(l, "zzEND")) {
			t.Errorf("a row of the reason was cut with an ellipsis: %q", l)
		}
	}
}

func TestReadErrorsWrap(t *testing.T) {
	t.Parallel()

	t.Run("the conversation", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 80, 24)
		reads.convErr = errors.New(longReason)

		ciDeliver(a, press(a, keyRune('i')))

		wholeReason(t, shownText(a))
	})

	t.Run("the log page", func(t *testing.T) {
		t.Parallel()

		a, reads, _ := ciApp(t, true, 80, 24)
		reads.logErr = errors.New(longReason)

		ciDeliver(a, press(a, keyRune('l')))

		wholeReason(t, shownText(a))
	})

	t.Run("the summary page", func(t *testing.T) {
		t.Parallel()

		a, reads, _ := sumApp(t, true, 80, 24, codeqlSummary)
		reads.runErr = errors.New(longReason)

		ciDeliver(a, press(a, keyRune('l')))

		wholeReason(t, shownText(a))
	})

	t.Run("the pull request list", func(t *testing.T) {
		t.Parallel()

		mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{searched("github.com/acme/web", 311, "Fix", "kari-dev", time.Hour)}}}
		a, reads, _ := searchApp(t, listAgents(), 80, 24, mine, "github.com/acme/web", "ghe.example.com/team/svc")
		reads.fail["ghe.example.com"] = errors.New(longReason)

		openList(a)
		typedList(a, "2")

		text := shownText(a)
		wholeReason(t, text)

		if !strings.Contains(text, "#311") {
			t.Errorf("the rows above the failing host are gone:\n%s", text)
		}
	})
}

func TestWrapRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		s     string
		width int
		rows  int
		want  []string
	}{
		{"fits", "one two", 20, 3, []string{"one two"}},
		{"wraps at a space", "one two three", 8, 3, []string{"one two", "three"}},
		{"breaks a long word", "abcdefghij", 4, 5, []string{"abcd", "efgh", "ij"}},
		{"cuts the rest with an ellipsis", "one two three four", 8, 2, []string{"one two", "three…"}},
		{"one row", "one two three", 8, 1, []string{"one two…"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := wrapRows(tc.s, tc.width, tc.rows)
			if !slices.Equal(got, tc.want) {
				t.Errorf("wrapRows = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadErrorKeepsWhatFits(t *testing.T) {
	t.Parallel()

	a, reads := convApp(t, true, 80, 7)
	reads.convErr = errors.New(longReason)

	ciDeliver(a, press(a, keyRune('i')))

	text := shownText(a)
	if !strings.Contains(text, fmt.Sprintf(convReadFailed, "HTTP 502")) || strings.Contains(text, "zzEND") {
		t.Errorf("the error is not cut to the room:\n%s", text)
	}

	if !cutWord.MatchString(text) {
		t.Errorf("the last kept row has no ellipsis:\n%s", text)
	}
}

func TestPageHintsWaitForTheContent(t *testing.T) {
	t.Parallel()

	// The hint row is the row holding the browser and the close keys.
	hintRow := func(t *testing.T, a *App) (row string, kt keyTable) {
		t.Helper()

		kt = a.keys.checks

		for l := range strings.SplitSeq(shownText(a), "\n") {
			if strings.Contains(l, kt.hint("checks.browser")) && strings.Contains(l, kt.hint("checks.close")) {
				return l, kt
			}
		}

		t.Fatalf("no hint row:\n%s", shownText(a))

		return "", kt
	}

	tests := []struct {
		name string
		page func(t *testing.T) *App
	}{
		{"log loading", func(t *testing.T) *App {
			a, _, _ := ciApp(t, true, 100, 30)
			press(a, keyRune('l'))

			return a
		}},
		{"log failed", func(t *testing.T) *App {
			a, reads, _ := ciApp(t, true, 100, 30)
			reads.logErr = errors.New("boom")

			ciDeliver(a, press(a, keyRune('l')))

			return a
		}},
		{"summary loading", func(t *testing.T) *App {
			a, _, _ := sumApp(t, true, 100, 30, codeqlSummary)
			press(a, keyRune('l'))

			return a
		}},
		{"summary failed", func(t *testing.T) *App {
			a, reads, _ := sumApp(t, true, 100, 30, codeqlSummary)
			reads.runErr = errors.New("boom")

			ciDeliver(a, press(a, keyRune('l')))

			return a
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row, kt := hintRow(t, tc.page(t))
			if line := kt.hint("checks.next", "checks.prev"); strings.Contains(row, line) {
				t.Errorf("hint row = %q, holds %q before the content loaded", row, line)
			}
		})
	}

	t.Run("loaded pages offer their keys", func(t *testing.T) {
		t.Parallel()

		a, _, _ := ciApp(t, true, 100, 30)
		ciPage(t, a)

		row, kt := hintRow(t, a)
		if line, fold := kt.hint("checks.next", "checks.prev"), kt.hint("checks.fold"); !strings.Contains(row, line) || !strings.Contains(row, fold) {
			t.Errorf("log hint row = %q, want %q and %q", row, line, fold)
		}

		b, _, _ := sumApp(t, true, 100, 30, codeqlSummary)
		sumPage(t, b)

		row, kt = hintRow(t, b)
		if line := kt.hint("checks.next", "checks.prev"); !strings.Contains(row, line) {
			t.Errorf("summary hint row = %q, want %q", row, line)
		}
	})
}
