package kitty

import (
	"encoding/base64"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestQuery(t *testing.T) {
	t.Parallel()

	want := "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[16t\x1b[c"
	if got := Query(); got != want {
		t.Fatalf("Query() = %q, want %q", got, want)
	}
}

func TestTransmit(t *testing.T) {
	t.Parallel()

	place := "\x1b_Ga=p,U=1,i=7,c=12,r=5,q=2\x1b\\"

	t.Run("single chunk", func(t *testing.T) {
		t.Parallel()

		png := []byte{1, 2, 3}
		b64 := base64.StdEncoding.EncodeToString(png)

		want := "\x1b_Ga=t,f=100,t=d,i=7,q=2,m=0;" + b64 + "\x1b\\" + place
		if got := Transmit(7, png, 12, 5); got != want {
			t.Fatalf("Transmit = %q, want %q", got, want)
		}
	})

	t.Run("three chunks", func(t *testing.T) {
		t.Parallel()

		png := make([]byte, 6145)

		b64 := base64.StdEncoding.EncodeToString(png)
		if len(b64) != 8196 {
			t.Fatalf("fixture base64 length %d", len(b64))
		}

		want := "\x1b_Ga=t,f=100,t=d,i=7,q=2,m=1;" + b64[:4096] + "\x1b\\" +
			"\x1b_Gm=1;" + b64[4096:8192] + "\x1b\\" +
			"\x1b_Gm=0;" + b64[8192:] + "\x1b\\" + place
		if got := Transmit(7, png, 12, 5); got != want {
			t.Fatalf("Transmit mismatch: got %d bytes, want %d", len(got), len(want))
		}
	})
}

func TestDelete(t *testing.T) {
	t.Parallel()

	if got, want := Delete(9), "\x1b_Ga=d,d=I,i=9,q=2\x1b\\"; got != want {
		t.Fatalf("Delete = %q, want %q", got, want)
	}
}

func TestPlaceholders(t *testing.T) {
	t.Parallel()

	want := "\U0010EEEE\u030E\u0305\U0010EEEE\u030E\u030D\U0010EEEE\u030E\u030E"
	if got := Placeholders(2, 3); got != want {
		t.Fatalf("Placeholders(2, 3) = %q, want %q", got, want)
	}

	if w := ansi.StringWidth(Placeholders(0, 100)); w != 100 {
		t.Fatalf("width = %d, want 100", w)
	}
}

func TestIDColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id   uint32
		want string
	}{
		{1, "#000001"},
		{0xABCDEF, "#abcdef"},
		{MaxID + 2, "#000001"},
	}
	for _, tt := range tests {
		if got := IDColor(tt.id); got != tt.want {
			t.Errorf("IDColor(%#x) = %q, want %q", tt.id, got, tt.want)
		}
	}
}
