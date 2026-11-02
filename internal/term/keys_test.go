package term

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestEncodeLegacy(t *testing.T) {
	appCursor := KeyModes{AppCursor: true}
	appKeypad := KeyModes{AppKeypad: true}
	key := func(code rune, mod uv.KeyMod) uv.KeyPressEvent {
		return uv.KeyPressEvent{Code: code, Mod: mod}
	}

	tests := []struct {
		name  string
		key   uv.KeyPressEvent
		modes KeyModes
		want  string
	}{
		{"up", key(uv.KeyUp, 0), KeyModes{}, "\x1b[A"},
		{"up app cursor", key(uv.KeyUp, 0), appCursor, "\x1bOA"},
		{"shift+up", key(uv.KeyUp, uv.ModShift), KeyModes{}, "\x1b[1;2A"},
		{"alt+up", key(uv.KeyUp, uv.ModAlt), KeyModes{}, "\x1b[1;3A"},
		{"ctrl+shift+up", key(uv.KeyUp, uv.ModCtrl|uv.ModShift), KeyModes{}, "\x1b[1;6A"},
		{"meta+up", key(uv.KeyUp, uv.ModMeta), KeyModes{}, "\x1b[1;9A"},
		{"ctrl+up app cursor", key(uv.KeyUp, uv.ModCtrl), appCursor, "\x1b[1;5A"},
		{"home app cursor", key(uv.KeyHome, 0), appCursor, "\x1bOH"},
		{"ctrl+home", key(uv.KeyHome, uv.ModCtrl), KeyModes{}, "\x1b[1;5H"},
		{"f1", key(uv.KeyF1, 0), KeyModes{}, "\x1bOP"},
		{"shift+f1", key(uv.KeyF1, uv.ModShift), KeyModes{}, "\x1b[1;2P"},
		{"f5", key(uv.KeyF5, 0), KeyModes{}, "\x1b[15~"},
		{"ctrl+f5", key(uv.KeyF5, uv.ModCtrl), KeyModes{}, "\x1b[15;5~"},
		{"f12", key(uv.KeyF12, 0), KeyModes{}, "\x1b[24~"},
		{"pgup", key(uv.KeyPgUp, 0), KeyModes{}, "\x1b[5~"},
		{"delete", key(uv.KeyDelete, 0), KeyModes{}, "\x1b[3~"},
		{"enter", key(uv.KeyEnter, 0), KeyModes{}, "\r"},
		{"alt+shift+.", uv.KeyPressEvent{Code: '.', ShiftedCode: '>', Mod: uv.ModAlt | uv.ModShift}, KeyModes{}, "\x1b>"},
		{"alt+shift+. no shifted", key('.', uv.ModAlt|uv.ModShift), KeyModes{}, "\x1b."},
		{"ctrl+2", key('2', uv.ModCtrl), KeyModes{}, "\x00"},
		{"ctrl+7", key('7', uv.ModCtrl), KeyModes{}, "\x1f"},
		{"ctrl+4", key('4', uv.ModCtrl), KeyModes{}, "\x1c"},
		{"ctrl+/", key('/', uv.ModCtrl), KeyModes{}, "\x1f"},
		{"ctrl+8", key('8', uv.ModCtrl), KeyModes{}, "\x7f"},
		{"ctrl+shift+\\", uv.KeyPressEvent{Code: '\\', ShiftedCode: '|', Mod: uv.ModCtrl | uv.ModShift}, KeyModes{}, "\x1c"},
		{"ctrl+shift+/", uv.KeyPressEvent{Code: '/', ShiftedCode: '?', Mod: uv.ModCtrl | uv.ModShift}, KeyModes{}, "\x7f"},
		{"alt+enter", key(uv.KeyEnter, uv.ModAlt), KeyModes{}, "\x1b\r"},
		{"shift+enter", key(uv.KeyEnter, uv.ModShift), KeyModes{}, "\r"},
		{"tab", key(uv.KeyTab, 0), KeyModes{}, "\t"},
		{"shift+tab", key(uv.KeyTab, uv.ModShift), KeyModes{}, "\x1b[Z"},
		{"backspace", key(uv.KeyBackspace, 0), KeyModes{}, "\x7f"},
		{"alt+backspace", key(uv.KeyBackspace, uv.ModAlt), KeyModes{}, "\x1b\x7f"},
		{"ctrl+backspace", key(uv.KeyBackspace, uv.ModCtrl), KeyModes{}, "\x08"},
		{"esc", key(uv.KeyEscape, 0), KeyModes{}, "\x1b"},
		{"ctrl+a", key('a', uv.ModCtrl), KeyModes{}, "\x01"},
		{"ctrl+[", key('[', uv.ModCtrl), KeyModes{}, "\x1b"},
		{"ctrl+space", key(uv.KeySpace, uv.ModCtrl), KeyModes{}, "\x00"},
		{"alt+a", key('a', uv.ModAlt), KeyModes{}, "\x1ba"},
		{"ctrl+alt+a", key('a', uv.ModCtrl|uv.ModAlt), KeyModes{}, "\x1b\x01"},
		{"a", uv.KeyPressEvent{Code: 'a', Text: "a"}, KeyModes{}, "a"},
		{"text é", uv.KeyPressEvent{Code: 'é', Text: "é"}, KeyModes{}, "é"},
		{"repeat up", uv.KeyPressEvent{Code: uv.KeyUp, IsRepeat: true}, KeyModes{}, "\x1b[A"},
		{"keypad 5", key(uv.KeyKp5, 0), KeyModes{}, "5"},
		{"keypad 5 app", key(uv.KeyKp5, 0), appKeypad, "\x1bOu"},
		{"keypad enter app", key(uv.KeyKpEnter, 0), appKeypad, "\x1bOM"},
		{"keypad up", key(uv.KeyKpUp, 0), KeyModes{}, "\x1b[A"},
		{"f13", key(uv.KeyF13, 0), KeyModes{}, ""},
		{"shift alone", key(uv.KeyLeftShift, uv.ModShift), KeyModes{}, ""},
		{"empty", uv.KeyPressEvent{}, KeyModes{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncodeKey(tt.key, tt.modes)
			if string(got) != tt.want {
				t.Fatalf("EncodeKey = %q; want %q", got, tt.want)
			}

			if tt.want == "" && got != nil {
				t.Fatalf("EncodeKey = %#v; want nil", got)
			}
		})
	}
}

func TestEncodeKitty(t *testing.T) {
	tests := []struct {
		name  string
		key   uv.KeyPressEvent
		modes KeyModes
		want  string
	}{
		{"ctrl+c", uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl}, KeyModes{KittyFlags: 1}, "\x1b[99;5u"},
		{"shift+enter", uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModShift}, KeyModes{KittyFlags: 1}, "\x1b[13;2u"},
		{"plain a falls back to text", uv.KeyPressEvent{Code: 'a', Text: "a"}, KeyModes{KittyFlags: 1}, "a"},
		{"up falls back to legacy", uv.KeyPressEvent{Code: uv.KeyUp}, KeyModes{KittyFlags: 1}, "\x1b[A"},
		{"modifyOtherKeys ctrl+c", uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl}, KeyModes{ModifyOtherKeys: 2}, "\x1b[27;5;99~"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeKey(tt.key, tt.modes); string(got) != tt.want {
				t.Fatalf("EncodeKey = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestEncodePaste(t *testing.T) {
	in := "a\x1b[201~b"

	if got := string(EncodePaste(in, false)); got != "a[201~b" {
		t.Fatalf("unbracketed = %q", got)
	}

	if got := string(EncodePaste(in, true)); got != "\x1b[200~a[201~b\x1b[201~" {
		t.Fatalf("bracketed = %q", got)
	}
}

func TestVTSendKey(t *testing.T) {
	v, reply := newTestVT(t, 10, 3)

	if _, err := v.Write([]byte("\x1b[?1h")); err != nil {
		t.Fatal(err)
	}

	v.SendKey(uv.KeyPressEvent{Code: uv.KeyF13})
	v.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})

	waitFor(t, func() bool { return reply.String() == "\x1bOA" })
}
