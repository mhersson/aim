package term

import "testing"

func TestDAQueries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		chunks []string
		want   int
	}{
		{"plain", []string{"\x1b[c"}, 1},
		{"with zero", []string{"a\x1b[0cb"}, 1},
		{"two", []string{"\x1b[c\x1b[0c"}, 2},
		{"split after esc", []string{"\x1b", "[c"}, 1},
		{"split before c", []string{"x\x1b[0", "c"}, 1},
		{"not counted twice", []string{"\x1b[c", "z"}, 1},
		{"other sequence", []string{"\x1b[6n\x1b[?u"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var carry []byte

			got := 0

			for _, c := range tt.chunks {
				var n int

				n, carry = daQueries(carry, []byte(c))
				got += n
			}

			if got != tt.want {
				t.Errorf("queries = %d, want %d", got, tt.want)
			}
		})
	}
}
