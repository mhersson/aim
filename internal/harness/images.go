package harness

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// MaxImages is the most images one batch carries.
	MaxImages = 8
	// MaxNamed is the most final-reply paths an event carries before AIM checks they exist.
	MaxNamed = 32
	// MaxImagePath leaves out a path no real file has.
	MaxImagePath = 1024
	// maxImageBytes bounds the JSON-encoded paths like maxChangedBytes.
	maxImageBytes = 32 << 10
)

// IsImagePath reports whether path has an extension AIM can show.
func IsImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}

	return false
}

// Caption makes s a caption: white space runs become one space, then clip.
func Caption(s string) string { return clip(strings.Join(strings.Fields(s), " ")) }

// imagePaths resolves paths against cwd (when absolute), cleans them and keeps
// the images in order, without repeats, within limit and maxImageBytes.
func imagePaths(paths []string, cwd string, limit int) []string {
	var out []string

	seen := map[string]bool{}
	size := 0

	for _, p := range paths {
		if !filepath.IsAbs(p) && filepath.IsAbs(cwd) {
			p = filepath.Join(cwd, p)
		}

		if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
			continue
		}

		p = filepath.Clean(p)
		if len(p) > MaxImagePath || !IsImagePath(p) || seen[p] {
			continue
		}

		enc, err := json.Marshal(p)
		if err != nil {
			continue
		}

		if size += len(enc) + 1; size > maxImageBytes {
			break
		}

		seen[p] = true

		if out = append(out, p); len(out) == limit {
			break
		}
	}

	return out
}

var mdLink = regexp.MustCompile(`!?\[[^\]\n]*\]\(\s*<?([^)>\s]+)>?(?:\s+"[^"]*")?\s*\)`)

// namedImages reads the image paths a final reply names: markdown link targets
// and bare or backticked tokens, with ~/ taken from home.
func namedImages(text, cwd, home string) []string {
	text = mdLink.ReplaceAllString(text, " $1 ")
	text = strings.ReplaceAll(text, "`", " ")

	var tokens []string

	for tok := range strings.FieldsSeq(text) {
		tok = strings.TrimRight(strings.TrimLeft(tok, "([{<\"'"), ")]}>\"',;:.!?")
		if strings.Contains(tok, "://") || strings.HasPrefix(tok, "-") || !IsImagePath(tok) {
			continue
		}

		if rest, ok := strings.CutPrefix(tok, "~/"); ok {
			if home == "" {
				continue
			}

			tok = filepath.Join(home, rest)
		}

		tokens = append(tokens, tok)
	}

	return imagePaths(tokens, cwd, MaxNamed)
}
