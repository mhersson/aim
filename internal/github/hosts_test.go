package github

import (
	"slices"
	"testing"
)

func TestNormalHosts(t *testing.T) {
	t.Parallel()

	got := normalHosts([]string{"tenant.ghe.com", "GitHub.com", "github.com"})
	if want := []string{"github.com", "tenant.ghe.com"}; !slices.Equal(got, want) {
		t.Errorf("normalHosts = %v, want %v", got, want)
	}
}
