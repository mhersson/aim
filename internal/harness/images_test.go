package harness

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestIsImagePath(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]bool{
		"a.png": true, "A.JPG": true, "x.jpeg": true, "x.gif": true, "x.webp": true,
		"x.svg": false, "x.png.txt": false, "png": false, "": false,
	} {
		if got := IsImagePath(path); got != want {
			t.Errorf("IsImagePath(%q) = %v, want %v", path, got, want)
		}
	}
}

func distinct(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("/d/%d.png", i)
	}

	return out
}

func TestImagePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		paths []string
		cwd   string
		want  []string
	}{
		{"relative", []string{"shots/a.png"}, "/work", []string{"/work/shots/a.png"}},
		{"dotdot", []string{"../a.png"}, "/work", []string{"/a.png"}},
		{"absolute", []string{"/tmp/b.gif"}, "/work", []string{"/tmp/b.gif"}},
		{"not an image", []string{"notes.md"}, "/work", nil},
		{"nul", []string{"/a\x00.png"}, "/work", nil},
		{"repeat", []string{"a.png", "/work/a.png"}, "/work", []string{"/work/a.png"}},
		{"too long", []string{"/" + strings.Repeat("a", 1020) + ".png"}, "/work", nil},
		{"limit", distinct(9), "/work", distinct(8)},
		{"relative cwd", []string{"a.png", "/b.png"}, "rel", []string{"/b.png"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := imagePaths(tt.paths, tt.cwd, MaxImages); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNamedImages(t *testing.T) {
	t.Parallel()

	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf("/m/%d.png", i)
	}

	tests := []struct {
		name, text, home string
		want             []string
	}{
		{"image link", "![shot](shots/x.png)", "/home/user", []string{"/work/shots/x.png"}},
		{"link title", `[a](x.png "title")`, "/home/user", []string{"/work/x.png"}},
		{"link angle", "[a](<x.png>)", "/home/user", []string{"/work/x.png"}},
		{"backticks", "`./y.jpg`", "/home/user", []string{"/work/y.jpg"}},
		{"sentence", "see /tmp/z.webp.", "/home/user", []string{"/tmp/z.webp"}},
		{"home", "~/q.gif", "/home/user", []string{"/home/user/q.gif"}},
		{"no home", "~/q.gif", "", nil},
		{"url", "https://h/x.png", "/home/user", nil},
		{"dash", "-x.png", "/home/user", nil},
		{"not image", "a.txt", "/home/user", nil},
		{
			"fixture text",
			"Here is ![shot](proof.png), `./proof.png`, ~/aim-proof-none.png and https://example.com/x.png.",
			"/home/user",
			[]string{"/work/proof.png", "/home/user/aim-proof-none.png"},
		},
		{"cap", strings.Join(many, " "), "/home/user", many[:32]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := namedImages(tt.text, "/work", tt.home); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCaption(t *testing.T) {
	t.Parallel()

	if got := Caption("a \n\t b\x07"); got != "a b" {
		t.Errorf("Caption = %q", got)
	}
}
