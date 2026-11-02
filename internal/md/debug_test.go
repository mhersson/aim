package md

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenerate the golden files in testdata/")

// roleNames names each Role for the debug form.
var roleNames = [...]string{
	RoleText:           "Text",
	RoleHeading1:       "Heading1",
	RoleHeading2:       "Heading2",
	RoleHeading3:       "Heading3",
	RoleRule:           "Rule",
	RoleCode:           "Code",
	RoleLink:           "Link",
	RoleKey:            "Key",
	RoleBullet:         "Bullet",
	RoleTaskDone:       "TaskDone",
	RoleTaskDoneText:   "TaskDoneText",
	RoleTaskOpen:       "TaskOpen",
	RoleCodeFrame:      "CodeFrame",
	RoleCodeLang:       "CodeLang",
	RoleLineNumber:     "LineNumber",
	RoleCodeText:       "CodeText",
	RoleHidden:         "Hidden",
	RoleQuoteBar:       "QuoteBar",
	RoleQuoteText:      "QuoteText",
	RoleAlertNote:      "AlertNote",
	RoleAlertTip:       "AlertTip",
	RoleAlertImportant: "AlertImportant",
	RoleAlertWarning:   "AlertWarning",
	RoleAlertCaution:   "AlertCaution",
	RoleSummaryMark:    "SummaryMark",
	RoleSummary:        "Summary",
	RoleFoldHint:       "FoldHint",
	RoleImage:          "Image",
	RoleImageHost:      "ImageHost",
	RolePicture:        "Picture",
	RoleTableFrame:     "TableFrame",
	RoleTableHeader:    "TableHeader",
	RoleFootnoteRef:    "FootnoteRef",
	RoleFootnoteRule:   "FootnoteRule",
	RoleFootnoteNumber: "FootnoteNumber",
	RoleFootnoteText:   "FootnoteText",
}

// debug renders l as one text line per layout line, each styled run as
// ‹Role[,b][,i][,u][,s]:text› and a plain RoleText run as its bare text. Runs
// that differ only in what a copy does with them print as one.
func (l Layout) debug() string {
	var b strings.Builder

	for _, line := range l.Lines {
		for _, r := range mergeCopyMarks(line) {
			attrs := ""

			for _, a := range []struct {
				on  bool
				tag string
			}{{r.Bold, ",b"}, {r.Italic, ",i"}, {r.Underline, ",u"}, {r.Strike, ",s"}} {
				if a.on {
					attrs += a.tag
				}
			}

			if r.Role == RoleText && attrs == "" {
				b.WriteString(r.Text)

				continue
			}

			b.WriteString("‹" + roleNames[r.Role] + attrs + ":" + r.Text + "›")
		}

		b.WriteByte('\n')
	}

	return b.String()
}

// mergeCopyMarks joins adjacent runs that differ only in the fields a copy reads.
func mergeCopyMarks(line Line) Line {
	var out Line

	bare := func(r Run) Run {
		r.Text, r.chrome, r.tab, r.rest, r.join = "", false, false, "", JoinNone

		return r
	}

	for _, r := range line {
		if n := len(out); n > 0 && bare(out[n-1]) == bare(r) {
			out[n-1].Text += r.Text

			continue
		}

		out = append(out, r)
	}

	return out
}

func TestRoleNamesComplete(t *testing.T) {
	t.Parallel()

	for i, n := range roleNames {
		if n == "" {
			t.Errorf("role %d has no debug name", i)
		}
	}
}

// goldenText compares got with testdata/<name>.golden, rewriting it under -update.
func goldenText(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}

		root, err := os.OpenRoot("testdata")
		if err != nil {
			t.Fatal(err)
		}

		defer root.Close()

		if err := root.WriteFile(name+".golden", []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}

	if !bytes.Equal([]byte(got), want) {
		t.Errorf("%s differs from the result (run with -update to accept):\n%s", path, got)
	}
}
