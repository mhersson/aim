package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCodexParseStatus(t *testing.T) {
	t.Parallel()

	const id = "01a20cf7-8253-76b2-8ef0-3d6b59580d38"

	base := func(mod func(*Event)) Event {
		ev := Event{Harness: "codex", Name: "statusline", Status: true, Session: id, Model: "gpt-test", RunState: "ready"}
		mod(&ev)

		return ev
	}

	tests := []struct {
		name    string
		file    string
		raw     string
		want    Event
		wantErr bool
	}{
		{name: "ready", file: "status-ready.json", want: base(func(*Event) {})},
		{name: "working named", file: "status-working-named.json", want: base(func(ev *Event) {
			ev.ThreadName, ev.RunState = "needs approval", "working"
		})},
		{name: "context", file: "status-context.json", want: base(func(ev *Event) {
			ev.Context, ev.ContextKnown, ev.Window, ev.InputTokens = 41.5, true, 258400, 107236
		})},
		{
			name: "hostile name",
			raw: `{"thread":{"id":"` + id + `","name":"a\u001b[31mb\u202e","agent":{"state":"ready"},` +
				`"model":{"display_name":"m"},"usage":{"context":{}}}}`,
			want: base(func(ev *Event) { ev.ThreadName, ev.Model = "a[31mb", "m" }),
		},
		{name: "bad thread id", raw: `{"thread":{"id":"x","agent":{"state":"ready"}}}`, wantErr: true},
		{name: "missing thread id", raw: `{"thread":{"agent":{"state":"ready"}}}`, wantErr: true},
		{name: "not json", raw: `not json`, wantErr: true},
		{
			name: "unknown state",
			raw:  `{"thread":{"id":"` + id + `","agent":{"state":"dancing"},"model":{},"usage":{"context":{}}}}`,
			want: base(func(ev *Event) { ev.Model, ev.RunState = "", "" }),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw := []byte(tt.raw)
			if tt.file != "" {
				var err error
				if raw, err = os.ReadFile(filepath.Join("testdata", "codex", tt.file)); err != nil {
					t.Fatal(err)
				}
			}

			ev, err := Codex{}.ParseStatus(raw)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseStatus = %+v, want an error", ev)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(ev, tt.want) {
				t.Errorf("event = %+v, want %+v", ev, tt.want)
			}
		})
	}
}
