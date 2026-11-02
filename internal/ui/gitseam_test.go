package ui

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

func TestBaseRefMissingIsExitStatusOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"exit 1", "exit 1", true},
		{"exit 2", "exit 2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := exec.Command("sh", "-c", tt.cmd).Run()
			if got := baseRefMissing(fmt.Errorf("resolve base: %w", err)); got != tt.want {
				t.Errorf("baseRefMissing(%v) = %v, want %v", err, got, tt.want)
			}
		})
	}

	if baseRefMissing(errors.New("x")) {
		t.Error("baseRefMissing(plain error) = true, want false")
	}
}
