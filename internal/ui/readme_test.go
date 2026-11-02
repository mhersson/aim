package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
)

const readmePath = "../../README.md"

// readmeKeyCommands are the commands of the README's Keys table, in order.
var readmeKeyCommands = []string{
	"agent.new", "rail.toggle", "review.toggle", "plan.open", "git.push",
	"pr.open", "pr.list", "pr.merge", "usage.toggle", "palette.open", "app.quit",
}

// readmeKeysTable is the README's Keys table, built from the default leader
// bindings and command titles.
func readmeKeysTable() (string, error) {
	reg, err := newRegistry(defaultCommands())
	if err != nil {
		return "", fmt.Errorf("build command registry: %w", err)
	}

	km, _, err := resolveKeys(core.DefaultConfig().Keys, reg)
	if err != nil {
		return "", fmt.Errorf("resolve default keys: %w", err)
	}

	var b strings.Builder

	b.WriteString("| Keys | Does |\n| --- | --- |\n")

	for _, id := range readmeKeyCommands {
		var keys []string

		for _, bd := range km.bindings {
			if bd.command == id {
				keys = append(keys, bd.key)
			}
		}

		if len(keys) != 1 {
			return "", fmt.Errorf("command %s has %d leader bindings, want 1", id, len(keys))
		}

		c, ok := reg.get(id)
		if !ok {
			return "", fmt.Errorf("command %s is not registered", id)
		}

		fmt.Fprintf(&b, "| %s | %s |\n", mdCell(km.leader+" "+keys[0]), titleCell(c.title))
	}

	return b.String(), nil
}

// defaultLeaderKeys returns the default leader and every key bound after it.
func defaultLeaderKeys() (leader string, keys map[string]bool, err error) {
	reg, err := newRegistry(defaultCommands())
	if err != nil {
		return "", nil, fmt.Errorf("build command registry: %w", err)
	}

	km, _, err := resolveKeys(core.DefaultConfig().Keys, reg)
	if err != nil {
		return "", nil, fmt.Errorf("resolve default keys: %w", err)
	}

	keys = map[string]bool{}
	for _, bd := range km.bindings {
		keys[bd.key] = true
	}

	return km.leader, keys, nil
}

// sectionTable returns the first table under the "## heading" line, one
// "\n"-ended line each, or "" when the section has none.
func sectionTable(src, heading string) string {
	var b strings.Builder

	in := false

	for _, line := range guideLines(src) {
		switch {
		case !in:
			in = line == "## "+heading
		case strings.HasPrefix(line, "## "):
			return b.String()
		case strings.HasPrefix(line, "|"):
			b.WriteString(line + "\n")
		case b.Len() > 0:
			return b.String()
		}
	}

	return b.String()
}

// codeSpans returns the contents of a line's code spans, by the rules of
// stripCodeSpans.
func codeSpans(line string) []string {
	var out []string

	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++

			continue
		}

		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}

		end := strings.Index(line[i+n:], strings.Repeat("`", n))
		if end < 0 {
			i += n

			continue
		}

		out = append(out, line[i+n:i+n+end])
		i += n + end + n
	}

	return out
}

// leaderSpanProblems lists the code spans of src that read as the leader
// followed by a key that is not a default binding.
func leaderSpanProblems(src, leader string, keys map[string]bool) []string {
	var out []string

	for _, line := range guideLines(src) {
		for _, span := range codeSpans(line) {
			rest, ok := strings.CutPrefix(span, leader+" ")
			if ok && !keys[rest] {
				out = append(out, "README.md: `"+span+"` is not a default key")
			}
		}
	}

	return out
}

func TestSectionTable(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, src, want string }{
		{"under heading", "# T\n\n## Keys\n\ntext\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\n## Next\n", "| a | b |\n| - | - |\n| 1 | 2 |\n"},
		{"other heading", "## Other\n\n| a |\n\n## Keys\n\ntext\n", ""},
		{"table before next heading only", "## Keys\n\ntext\n\n## Other\n\n| a |\n", ""},
		{"ends at blank line", "## Keys\n| a |\n| b |\n\n| c |\n", "| a |\n| b |\n"},
		{"heading in fence", "```\n## Keys\n```\n| a |\n", ""},
		{"no heading", "| a |\n", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := sectionTable(tc.src, "Keys"); got != tc.want {
				t.Errorf("sectionTable = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCodeSpans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, line string
		want       []string
	}{
		{"spans", "a `x` b ``y`z`` c", []string{"x", "y`z"}},
		{"unclosed", "a `x b", nil},
		{"none", "plain", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := codeSpans(tc.line)
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") || len(got) != len(tc.want) {
				t.Errorf("codeSpans(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestLeaderSpanProblems(t *testing.T) {
	t.Parallel()

	keys := map[string]bool{"n": true, "g P": true}

	tests := []struct {
		name, src string
		want      int
	}{
		{"known keys", "`ctrl+space n` and `ctrl+space g P`", 0},
		{"bare leader", "press `ctrl+space`", 0},
		{"unknown key", "press `ctrl+space zz`", 1},
		{"in fence", "```\n`ctrl+space zz`\n```", 0},
		{"other span", "`q` alone", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := leaderSpanProblems(tc.src, "ctrl+space", keys)
			if len(got) != tc.want {
				t.Fatalf("problems = %q, want %d", got, tc.want)
			}

			if tc.want == 1 && !strings.Contains(got[0], "ctrl+space zz") {
				t.Errorf("problem %q does not name the span", got[0])
			}
		})
	}
}

func TestReadmeKeys(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}

	src := string(raw)

	t.Run("table", func(t *testing.T) {
		t.Parallel()

		want, err := readmeKeysTable()
		if err != nil {
			t.Fatal(err)
		}

		if got := sectionTable(src, "Keys"); got != want {
			t.Fatalf("README.md's Keys table is stale; replace it with:\n%s\ngot:\n%s", want, got)
		}
	})

	t.Run("leader keys", func(t *testing.T) {
		t.Parallel()

		leader, keys, err := defaultLeaderKeys()
		if err != nil {
			t.Fatal(err)
		}

		for _, p := range leaderSpanProblems(src, leader, keys) {
			t.Error(p)
		}
	})
}
