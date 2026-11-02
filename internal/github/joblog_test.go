package github

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/git/invisible"
)

func lineText(l LogLine) string {
	var b strings.Builder
	for _, s := range l.Spans {
		b.WriteString(s.Text)
	}

	return b.String()
}

func TestParseJobLogLines(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 30, 12, 1, 5, 123456700, time.UTC)

	tests := []struct {
		name string
		in   string
		cut  bool
		want []string
		at   time.Time
	}{
		{"timestamp", "2026-10-30T12:01:05.1234567Z make check\n", false, []string{"make check"}, at},
		{"plain", "plain\n", false, []string{"plain"}, time.Time{}},
		{"cut", "partial\nwhole\n", true, []string{"whole"}, time.Time{}},
		{"bom", "\xef\xbb\xbfhi\n", false, []string{"hi"}, time.Time{}},
		{"crlf", "a\r\n", false, []string{"a"}, time.Time{}},
		{"progress", "10%\r50%\rdone", false, []string{"done"}, time.Time{}},
		{"no final newline", "a\nb", false, []string{"a", "b"}, time.Time{}},
		{"empty", "", false, nil, time.Time{}},
		{"bad time", "2026-13-30T12:01:05Z x\n", false, []string{"2026-13-30T12:01:05Z x"}, time.Time{}},
		{"timestamp only", "2026-10-30T12:01:05.1234567Z \n", false, []string{""}, at},
		{"unterminated osc then line", "a\x1b]0;t\nb\n", false, []string{"a", "b"}, time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := ParseJobLog([]byte(tc.in), tc.cut)
			if len(log.Lines) != len(tc.want) {
				t.Fatalf("%d lines, want %d: %+v", len(log.Lines), len(tc.want), log.Lines)
			}

			for i, w := range tc.want {
				if got := lineText(log.Lines[i]); got != w {
					t.Errorf("line %d = %q, want %q", i, got, w)
				}
			}

			if len(tc.want) > 0 && !log.Lines[0].At.Equal(tc.at) {
				t.Errorf("At = %v, want %v", log.Lines[0].At, tc.at)
			}

			if log.Cut != tc.cut {
				t.Errorf("Cut = %v", log.Cut)
			}
		})
	}
}

func TestParseJobLogMarkers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		kind LogKind
		text string
	}{
		{"##[error]Process completed with exit code 2.", LogError, "Error: Process completed with exit code 2."},
		{"##[warning]Node 16 is old", LogWarning, "Warning: Node 16 is old"},
		{"##[command]/usr/bin/git fetch", LogCommand, "/usr/bin/git fetch"},
		{"##[debug]Evaluating: x", LogDebug, "Evaluating: x"},
		{"x ##[error]not at the start", LogText, "x ##[error]not at the start"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			log := ParseJobLog([]byte("2026-10-30T12:00:00Z "+tc.in+"\n"), false)
			if len(log.Lines) != 1 {
				t.Fatalf("%d lines", len(log.Lines))
			}

			l := log.Lines[0]
			if l.Kind != tc.kind || lineText(l) != tc.text {
				t.Errorf("got kind %d %q, want %d %q", l.Kind, lineText(l), tc.kind, tc.text)
			}
		})
	}

	log := ParseJobLog([]byte("a\n##[warning]w\n##[error]e1\n##[error]e2\n"), false)
	if log.FirstError != 2 {
		t.Errorf("FirstError = %d, want 2", log.FirstError)
	}

	if got := ParseJobLog([]byte("a\n"), false).FirstError; got != -1 {
		t.Errorf("FirstError without an error = %d", got)
	}
}

func TestParseJobLogGroups(t *testing.T) {
	t.Parallel()

	in := "before\n" +
		"##[group]One\n" +
		"a\n" +
		"##[endgroup]\n" +
		"between\n" +
		"##[group]Two\n" +
		"b\n" +
		"##[group]Three\n" +
		"c\n" +
		"d\n"
	log := ParseJobLog([]byte(in), false)
	texts := make([]string, len(log.Lines))

	groups := make([]int, len(log.Lines))
	for i, l := range log.Lines {
		texts[i], groups[i] = lineText(l), l.Group
	}

	wantTexts := "before One a between Two b Three c d"
	if got := strings.Join(texts, " "); got != wantTexts {
		t.Errorf("lines = %q, want %q", got, wantTexts)
	}

	wantGroups := []int{-1, 0, 0, -1, 1, 1, 2, 2, 2}
	for i, g := range wantGroups {
		if groups[i] != g {
			t.Errorf("line %d group = %d, want %d", i, groups[i], g)
		}
	}

	want := []LogGroup{{"One", 1, 3}, {"Two", 4, 6}, {"Three", 6, 9}}
	if len(log.Groups) != len(want) {
		t.Fatalf("groups = %+v", log.Groups)
	}

	for i, w := range want {
		if log.Groups[i] != w {
			t.Errorf("group %d = %+v, want %+v", i, log.Groups[i], w)
		}
	}

	if log.Lines[1].Kind != LogGroupHead {
		t.Errorf("head kind = %d", log.Lines[1].Kind)
	}
}

func TestParseJobLogColours(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []LogSpan
	}{
		{
			"bold cyan then reset", "\x1b[36;1mmake check\x1b[0m done",
			[]LogSpan{{"make check", 6, true}, {" done", -1, false}},
		},
		{"bright red", "\x1b[91mred", []LogSpan{{"red", 9, false}}},
		{"256 low", "\x1b[38;5;3mx", []LogSpan{{"x", 3, false}}},
		{"256 high", "\x1b[38;5;196mx", []LogSpan{{"x", -1, false}}},
		{"truecolour", "\x1b[38;2;1;2;3mx", []LogSpan{{"x", -1, false}}},
		{"background 256 keeps fg", "\x1b[31;48;5;2mx", []LogSpan{{"x", 1, false}}},
		{"default colour", "\x1b[31ma\x1b[39mb", []LogSpan{{"a", 1, false}, {"b", -1, false}}},
		{"empty resets", "\x1b[1;31ma\x1b[mb", []LogSpan{{"a", 1, true}, {"b", -1, false}}},
		{"unterminated", "a\x1b[31", []LogSpan{{"a", -1, false}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := ParseJobLog([]byte(tc.in+"\n"), false)
			if len(log.Lines) != 1 {
				t.Fatalf("%d lines", len(log.Lines))
			}

			got := log.Lines[0].Spans
			if len(got) != len(tc.want) {
				t.Fatalf("spans = %+v, want %+v", got, tc.want)
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("span %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseJobLogEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"osc 52", "\x1b]52;c;aGk=\x07a", "a"},
		{"osc 8", "\x1b]8;;https://x\x1b\\b", "b"},
		{"cursor", "\x1b[2Kc", "c"},
		{"unterminated osc", "g\x1b]0;title", "g"},
		{"dcs", "\x1bPq#0;2;0;0;0\x1b\\h", "h"},
		{"charset", "\x1b(Bi", "i"},
		{"del and nul", "j\x7fk\x00l", "jkl"},
		{"c1", "m\u0085n\u009bo\u009dp", "mnop"},
		{"invisible", "q\xe2\x80\xaer\xe2\x80\x8bs\xef\xbb\xbf", "qrs"},
		{"invalid utf8", "t\xffu\xc3v\x9bw", "tuvw"},
		{"lone esc", "x\x1b", "x"},
		{"csi intermediate", "\x1b[1 qz", "z"},
		{"esc in csi", "y\x1b[3\x1b[31mz", "yz"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := ParseJobLog([]byte(tc.in+"\n"), false)
			if len(log.Lines) != 1 {
				t.Fatalf("%d lines", len(log.Lines))
			}

			if got := lineText(log.Lines[0]); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}

			for _, s := range log.Lines[0].Spans {
				for _, r := range s.Text {
					if (r < 0x20 && r != '\t') || (r >= 0x7f && r <= 0x9f) || invisible.Rune(r) {
						t.Errorf("span %q holds %U", s.Text, r)
					}
				}
			}
		})
	}
}

func TestParseJobLogGroupTitleColour(t *testing.T) {
	t.Parallel()

	log := ParseJobLog([]byte("##[group]\x1b[36;1mBuild\x1b[0m step\nx\n"), false)
	if len(log.Groups) != 1 || log.Groups[0].Title != "Build step" {
		t.Fatalf("groups = %+v", log.Groups)
	}

	if got := log.Lines[0].Spans[0]; got.Color != 6 || !got.Bold {
		t.Errorf("head span = %+v, want cyan bold", got)
	}
}

func TestParseJobLogFixture(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/joblog.txt")
	if err != nil {
		t.Fatal(err)
	}

	log := ParseJobLog(data, false)
	if len(log.Groups) != 4 {
		t.Fatalf("%d groups: %+v", len(log.Groups), log.Groups)
	}

	if log.FirstError < 0 {
		t.Fatal("no error line")
	}

	e := log.Lines[log.FirstError]
	if e.Kind != LogError || lineText(e) != "Error: Process completed with exit code 2." {
		t.Errorf("error line = %+v", e)
	}

	if got := log.Groups[len(log.Groups)-1].Title; got != "Run make check" {
		t.Errorf("last group = %q", got)
	}

	if log.Lines[0].At.IsZero() {
		t.Error("first line has no time")
	}

	for i, l := range log.Lines {
		if l.At.IsZero() {
			t.Errorf("line %d has no time", i)
		}
	}

	var cmd bool

	for _, l := range log.Lines {
		if len(l.Spans) > 0 && l.Spans[0].Color == 6 && l.Spans[0].Bold {
			cmd = true
		}
	}

	if !cmd {
		t.Error("no bold cyan command line")
	}
}
