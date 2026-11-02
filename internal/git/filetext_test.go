package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func hashBlob(t *testing.T, dir, content string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return gitOut(t, dir, "hash-object", "-w", p)
}

func TestFileTextReadsBothSides(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	e := NewEngine(EngineOptions{})
	oldOID := hashBlob(t, dir, "a\nb\n")
	newOID := hashBlob(t, dir, "a\nc\nd")

	tests := []struct {
		name     string
		f        FileDiff
		old, new []string
	}{
		{"modified", FileDiff{Path: "f", OldOID: oldOID, NewOID: newOID}, []string{"a", "b"}, []string{"a", "c", "d"}},
		{"added", FileDiff{Path: "f", NewOID: newOID}, nil, []string{"a", "c", "d"}},
		{"deleted", FileDiff{Path: "f", OldOID: oldOID}, []string{"a", "b"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			old, nw, err := e.FileText(context.Background(), dir, tt.f)
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(old, tt.old) || !slices.Equal(nw, tt.new) {
				t.Errorf("got %q, %q; want %q, %q", old, nw, tt.old, tt.new)
			}
		})
	}
}

func TestFileTextTooLarge(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	e := NewEngine(EngineOptions{})
	e.maxText = 64
	big := hashBlob(t, dir, string(make([]byte, 65)))
	edge := hashBlob(t, dir, string(make([]byte, 64)))
	under := hashBlob(t, dir, string(make([]byte, 63)))
	small := hashBlob(t, dir, "x\n")

	tests := []struct {
		name string
		f    FileDiff
		want error
	}{
		{"old side over", FileDiff{OldOID: big, NewOID: small}, ErrTooLarge},
		{"new side over", FileDiff{OldOID: small, NewOID: big}, ErrTooLarge},
		{"exactly the limit", FileDiff{NewOID: edge}, nil},
		{"one under the limit", FileDiff{NewOID: under}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := e.FileText(context.Background(), dir, tt.f)
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
