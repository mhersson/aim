package core

import "testing"

func TestStoreSetTheme(t *testing.T) {
	tests := []struct {
		name      string
		action    SetTheme
		wantErr   bool
		wantTheme string
	}{
		{"valid name", SetTheme{Name: "catppuccin-latte"}, false, "catppuccin-latte"},
		{"empty name", SetTheme{}, true, "catppuccin-mocha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore(State{Theme: "catppuccin-mocha"})

			err := s.Dispatch(tt.action)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Dispatch() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got := s.State().Theme; got != tt.wantTheme {
				t.Errorf("State().Theme = %q, want %q", got, tt.wantTheme)
			}
		})
	}
}

func TestStoreRail(t *testing.T) {
	tests := []struct {
		name   string
		start  bool
		action Action
		want   bool
	}{
		{"toggle opens", false, ToggleRail{}, true},
		{"toggle closes", true, ToggleRail{}, false},
		{"set opens", false, SetRail{Open: true}, true},
		{"set closes", true, SetRail{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore(State{Rail: tt.start})

			if err := s.Dispatch(tt.action); err != nil {
				t.Fatalf("Dispatch() error = %v", err)
			}

			if got := s.State().Rail; got != tt.want {
				t.Errorf("State().Rail = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStateCloneKeepsRail(t *testing.T) {
	if !(State{Rail: true}).clone().Rail {
		t.Error("clone dropped Rail")
	}
}
