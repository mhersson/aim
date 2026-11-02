package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var marksNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func writeMarks(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMarks(t *testing.T) {
	t.Parallel()

	const common = "/repo/.git"

	tests := []struct {
		name    string
		body    string // "" means no file
		want    map[string]string
		wantBad bool
	}{
		{name: "no file", want: map[string]string{}},
		{
			name: "good file",
			body: `{"version":1,"repo":"/repo/.git","marks":{"a":"2026-10-29","b":"2026-10-30"}}`,
			want: map[string]string{"a": "2026-10-29", "b": "2026-10-30"},
		},
		{
			name: "expiry",
			body: `{"version":1,"repo":"/repo/.git","marks":{"old":"2026-07-31","keep":"2026-08-01","junk":"yesterday"}}`,
			want: map[string]string{"keep": "2026-08-01"},
		},
		{name: "invalid json", body: `{nope`, want: map[string]string{}, wantBad: true},
		{name: "version 2", body: `{"version":2,"repo":"/repo/.git","marks":{"a":"2026-10-30"}}`, want: map[string]string{}, wantBad: true},
		{name: "other repo", body: `{"version":1,"repo":"/other/.git","marks":{"a":"2026-10-30"}}`, want: map[string]string{}, wantBad: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "reviews", "x.json")
			if tt.body != "" {
				writeMarks(t, path, tt.body)
			}

			got, err := LoadMarks(path, common, marksNow)
			if (err != nil) != tt.wantBad {
				t.Fatalf("err = %v, want error %v", err, tt.wantBad)
			}

			if got == nil || len(got) != len(tt.want) {
				t.Fatalf("marks = %v, want %v", got, tt.want)
			}

			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("mark %s = %q, want %q", k, got[k], v)
				}
			}

			_, badErr := os.Stat(path + ".bad")
			if (badErr == nil) != tt.wantBad {
				t.Errorf(".bad present = %v, want %v", badErr == nil, tt.wantBad)
			}

			if tt.wantBad {
				if _, serr := os.Stat(path); serr == nil {
					t.Error("bad file is still in place")
				}
			}
		})
	}
}

func TestLoadMarksReplacesOlderBad(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reviews", "x.json")
	writeMarks(t, path+".bad", "old")
	writeMarks(t, path, "{nope")

	if _, err := LoadMarks(path, "/r/.git", marksNow); err == nil {
		t.Fatal("want an error")
	}

	b, err := os.ReadFile(path + ".bad")
	if err != nil || string(b) != "{nope" {
		t.Errorf(".bad = %q, %v", b, err)
	}
}

func TestEncodeMarks(t *testing.T) {
	t.Parallel()

	days := map[string]string{"b": "2026-10-30", "a": "2026-10-29"}

	first, err := EncodeMarks("/r/.git", days)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Index(string(first), `"a"`) > strings.Index(string(first), `"b"`) {
		t.Errorf("keys not sorted:\n%s", first)
	}

	path := filepath.Join(t.TempDir(), "x.json")
	writeMarks(t, path, string(first))

	got, err := LoadMarks(path, "/r/.git", marksNow)
	if err != nil || len(got) != 2 {
		t.Errorf("round trip = %v, %v", got, err)
	}
}

func TestMarkFiles(t *testing.T) {
	t.Parallel()

	read := func(t *testing.T, stateDir, common string) map[string]string {
		t.Helper()

		got, err := LoadMarks(MarksPath(stateDir, common), common, marksNow)
		if err != nil {
			t.Fatal(err)
		}

		return got
	}

	t.Run("latest wins and close flushes", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		common := filepath.Join(dir, "r", ".git")
		m := NewMarkFiles(dir, nil)

		m.Save(common, map[string]string{"a": "2026-10-30"})
		m.Save(common, map[string]string{"b": "2026-10-30"})

		if err := m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}

		got := read(t, dir, common)
		if len(got) != 1 || got["b"] != "2026-10-30" {
			t.Errorf("marks = %v, want only b", got)
		}

		m.Save(common, map[string]string{"c": "2026-10-30"})

		if got := read(t, dir, common); got["c"] != "" {
			t.Errorf("a Save after Close was written: %v", got)
		}
	})

	t.Run("caller map is copied", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		common := filepath.Join(dir, "r", ".git")
		m := NewMarkFiles(dir, nil)
		days := map[string]string{"a": "2026-10-30"}

		m.Save(common, days)
		days["z"] = "2026-10-30"

		if err := m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}

		if got := read(t, dir, common); len(got) != 1 {
			t.Errorf("marks = %v, want only a", got)
		}
	})

	t.Run("modes", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		common := filepath.Join(dir, "r", ".git")
		m := NewMarkFiles(dir, nil)
		m.Save(common, map[string]string{"a": "2026-10-30"})

		if err := m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}

		fi, err := os.Stat(MarksPath(dir, common))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("file = %v, %v; want 0600", fi, err)
		}

		di, err := os.Stat(filepath.Join(dir, "reviews"))
		if err != nil || di.Mode().Perm() != 0o700 {
			t.Errorf("dir = %v, %v; want 0700", di, err)
		}
	})

	t.Run("two repositories two files", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		c1, c2 := filepath.Join(dir, "r1", ".git"), filepath.Join(dir, "r2", ".git")
		m := NewMarkFiles(dir, nil)
		m.Save(c1, map[string]string{"a": "2026-10-30"})
		m.Save(c2, map[string]string{"b": "2026-10-30"})

		if err := m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}

		if g1, g2 := read(t, dir, c1), read(t, dir, c2); g1["a"] == "" || g1["b"] != "" || g2["b"] == "" || g2["a"] != "" {
			t.Errorf("marks = %v and %v", g1, g2)
		}
	})

	t.Run("save does no filesystem work", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		resolved := filepath.Join(dir, "real", ".git")
		common := filepath.Join(dir, "later", ".git")
		release := make(chan struct{})
		m := NewMarkFiles(dir, nil)
		// The writer waits here, so a Save that resolved the dir itself would never return.
		m.resolve = func(string) string {
			<-release

			return resolved
		}

		m.Save(common, map[string]string{"a": "2026-10-30"})
		close(release)

		if err := m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}

		if got := read(t, dir, resolved); got["a"] != "2026-10-30" {
			t.Errorf("marks under the resolved dir = %v, want a", got)
		}

		if _, err := os.Stat(MarksPath(dir, common)); err == nil {
			t.Error("marks written under the dir as given")
		}
	})

	t.Run("spellings of one repository, the later Save wins", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		resolved := filepath.Join(dir, "real", ".git")
		spell1, spell2, busy := filepath.Join(dir, "s1", ".git"), filepath.Join(dir, "s2", ".git"), filepath.Join(dir, "busy", ".git")

		for _, order := range [][2]string{{spell1, spell2}, {spell2, spell1}} {
			m := NewMarkFiles(dir, nil)
			release := make(chan struct{})
			// Holding the writer on busy puts both Saves in one batch.
			m.resolve = func(d string) string {
				if d == busy {
					<-release
				}

				return resolved
			}

			m.Save(busy, map[string]string{"x": "2026-10-30"})
			m.Save(order[0], map[string]string{"first": "2026-10-30"})
			m.Save(order[1], map[string]string{"second": "2026-10-30"})
			close(release)

			if err := m.Close(context.Background()); err != nil {
				t.Fatal(err)
			}

			if got := read(t, dir, resolved); len(got) != 1 || got["second"] == "" {
				t.Errorf("order %v: marks = %v, want only second", order, got)
			}
		}
	})

	t.Run("unreadable file is never overwritten", func(t *testing.T) {
		t.Parallel()

		if os.Geteuid() == 0 {
			t.Skip("root reads a mode 0000 file")
		}

		dir := t.TempDir()
		common := filepath.Join(dir, "r", ".git")
		path := MarksPath(dir, common)
		body := `{"version":1,"repo":"` + common + `","marks":{"a":"2026-10-01"}}`
		writeMarks(t, path, body)

		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatal(err)
		}

		m := NewMarkFiles(dir, nil)

		if _, err := m.Load(common, marksNow); err == nil {
			t.Fatal("Load of an unreadable file gave no error")
		}

		m.Save(common, map[string]string{"b": "2026-10-30"})

		if err := m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}

		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(path)
		if err != nil || string(got) != body {
			t.Errorf("file = %q, %v; want it unchanged", got, err)
		}
	})
}

func TestMarksAcrossSpellings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real", ".git")

	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(dir, "link", ".git")
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	m := NewMarkFiles(dir, nil)
	m.Save(linkDir, map[string]string{"a": "2026-10-30"})

	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, spelling := range []string{realDir, linkDir} {
		path := MarksPath(dir, spelling)

		got, err := LoadMarks(path, spelling, marksNow)
		if err != nil || got["a"] != "2026-10-30" {
			t.Errorf("LoadMarks via %s = %v, %v", spelling, got, err)
		}

		if _, serr := os.Stat(path + ".bad"); serr == nil {
			t.Errorf("LoadMarks via %s renamed the file to .bad", spelling)
		}
	}

	enc, err := EncodeMarks(linkDir, map[string]string{"a": "2026-10-30"})
	if err != nil {
		t.Fatal(err)
	}

	want, err := os.ReadFile(MarksPath(dir, realDir))
	if err != nil || string(enc) != string(want) {
		t.Errorf("EncodeMarks via a symlink differs from the written file: %v\n%s\n%s", err, enc, want)
	}
}
