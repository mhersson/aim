package rec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func steppingClock(start time.Time, step time.Duration) func() time.Time {
	t := start
	first := true

	return func() time.Time {
		if first {
			first = false

			return t
		}

		t = t.Add(step)

		return t
	}
}

func TestRecRoundTrip(t *testing.T) {
	var buf bytes.Buffer

	w, err := NewWriter(&buf, 80, 24, steppingClock(time.Unix(100, 0), 10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Data([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	if err := w.Resize(100, 30); err != nil {
		t.Fatal(err)
	}

	if err := w.Data([]byte("\x1b[31mred")); err != nil {
		t.Fatal(err)
	}

	got, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}

	want := []Record{
		{At: 0, Cols: 80, Rows: 24},
		{At: 10 * time.Millisecond, Data: []byte("hello")},
		{At: 20 * time.Millisecond, Cols: 100, Rows: 30},
		{At: 30 * time.Millisecond, Data: []byte("\x1b[31mred")},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}

func TestRecWriterRejectsOversize(t *testing.T) {
	var buf bytes.Buffer

	w, err := NewWriter(&buf, 80, 24, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Data(make([]byte, MaxChunk+1)); err == nil {
		t.Fatal("oversize chunk accepted")
	}
}

func typed(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func resizeRec(at time.Duration, cols, rows uint16) []byte {
	b := make([]byte, 13)
	b[0] = 'R'
	binary.BigEndian.PutUint64(b[1:], uint64(at))
	binary.BigEndian.PutUint16(b[9:], cols)
	binary.BigEndian.PutUint16(b[11:], rows)

	return b
}

func dataRec(at time.Duration, n uint32, payload string) []byte {
	b := make([]byte, 13, 13+len(payload))
	b[0] = 'D'
	binary.BigEndian.PutUint64(b[1:], uint64(at))
	binary.BigEndian.PutUint32(b[9:], n)

	return append(b, payload...)
}

func TestRecReadErrors(t *testing.T) {
	hdr := []byte(header)

	tests := []struct {
		name      string
		in        []byte
		wantTrunc bool
		wantRecs  int
	}{
		{name: "bad header", in: []byte("NOTREC 1\n")},
		{name: "empty", in: nil},
		{name: "no initial resize", in: typed(hdr, dataRec(0, 1, "x"))},
		{name: "unknown type", in: typed(hdr, resizeRec(0, 80, 24), append([]byte{'Z'}, make([]byte, 12)...)), wantRecs: 1},
		{name: "zero size", in: typed(hdr, resizeRec(0, 0, 24))},
		{name: "truncated header", in: typed(hdr, resizeRec(0, 80, 24), dataRec(1, 5, "hello")[:6]), wantTrunc: true, wantRecs: 1},
		{name: "truncated payload", in: typed(hdr, resizeRec(0, 80, 24), dataRec(1, 5, "he")), wantTrunc: true, wantRecs: 1},
		{name: "no records", in: hdr, wantTrunc: true},
		{name: "time backwards", in: typed(hdr, resizeRec(5, 80, 24), dataRec(4, 1, "x")), wantRecs: 1},
		{name: "oversize", in: typed(hdr, resizeRec(0, 80, 24), dataRec(1, MaxChunk+1, "")), wantRecs: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs, err := Read(bytes.NewReader(tt.in))
			if err == nil {
				t.Fatal("no error")
			}

			if got := errors.Is(err, ErrTruncated); got != tt.wantTrunc {
				t.Errorf("ErrTruncated = %v, want %v (err: %v)", got, tt.wantTrunc, err)
			}

			if len(recs) != tt.wantRecs {
				t.Errorf("records = %d, want %d", len(recs), tt.wantRecs)
			}
		})
	}
}

func TestRecReadSpikeFormat(t *testing.T) {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "VTSPIKE-REC 1 79 31\n")

	for _, c := range []struct {
		at   uint64
		data string
	}{{5, "ab"}, {9, "cde"}} {
		var h [12]byte
		binary.BigEndian.PutUint64(h[:], c.at)
		binary.BigEndian.PutUint32(h[8:], uint32(len(c.data)))
		buf.Write(h[:])
		buf.WriteString(c.data)
	}

	got, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}

	want := []Record{
		{Cols: 79, Rows: 31},
		{At: 5, Data: []byte("ab")},
		{At: 9, Data: []byte("cde")},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}

func TestRecReadSpikeFormatTruncated(t *testing.T) {
	in := append([]byte("VTSPIKE-REC 1 79 31\n"), 0, 0, 0)

	got, err := Read(bytes.NewReader(in))
	if !errors.Is(err, ErrTruncated) || len(got) != 1 {
		t.Fatalf("got %d records, err %v; want the initial resize and ErrTruncated", len(got), err)
	}
}

func TestSynthDeterministic(t *testing.T) {
	a := Synth(80, 24, time.Second, 7)
	b := Synth(80, 24, time.Second, 7)
	c := Synth(80, 24, time.Second, 8)

	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different streams")
	}

	if reflect.DeepEqual(a, c) {
		t.Fatal("different seeds produced the same stream")
	}

	if a[0].Data != nil || a[0].Cols != 80 || a[0].Rows != 24 {
		t.Fatalf("first record = %+v, want the initial resize", a[0])
	}

	if len(a) != 101 {
		t.Fatalf("records = %d, want 1 resize + 100 frames", len(a))
	}

	last := a[len(a)-1]
	if last.At != 990*time.Millisecond {
		t.Errorf("last frame at %v, want 990ms", last.At)
	}

	for _, want := range []string{"\x1b[?2026h", "\x1b[?2026l", "\x1b[38;2;"} {
		if !bytes.Contains(a[1].Data, []byte(want)) {
			t.Errorf("frame lacks %q", want)
		}
	}
}

func TestRecAtStampsGivenTime(t *testing.T) {
	var buf bytes.Buffer

	t0 := time.Unix(100, 0)

	w, err := NewWriter(&buf, 80, 24, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}

	if err := w.DataAt([]byte("x"), t0.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	if err := w.ResizeAt(10, 5, t0.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}

	recs, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if len(recs) != 3 || recs[1].At != 3*time.Second || recs[2].At != 4*time.Second {
		t.Errorf("records = %+v, want stamps 3 s and 4 s", recs)
	}
}
