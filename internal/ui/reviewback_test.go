package ui

import "testing"

func TestReviewBackSteps(t *testing.T) {
	t.Parallel()

	r := realZoomRig(t)
	st := r.store.State()
	half, _ := r.drawerHalf(st)
	body := r.bodyAreaIn(st.Rail)
	before := r.focus()

	r.keys(keyRune('z'))
	r.finishSlide()
	r.keys(keyTab)

	if !r.drawer.tree {
		t.Fatal("setup: tab did not focus the tree")
	}

	r.keys(keyEsc)

	if r.drawer.tree || !r.zoomed() || !r.drawerHasKeys() {
		t.Errorf("first esc: tree %v, zoomed %v, drawer has the keyboard %v, want the diff focused, still zoomed, keyboard kept", r.drawer.tree, r.zoomed(), r.drawerHasKeys())
	}

	r.keys(keyEsc)

	if r.zoomed() || !r.drawerHasKeys() || r.drawer.resizeFrom != body.W {
		t.Errorf("second esc: zoomed %v, drawer has the keyboard %v, resizeFrom %d, want an un-zoom slide from %d with the keyboard kept", r.zoomed(), r.drawerHasKeys(), r.drawer.resizeFrom, body.W)
	}

	r.finishSlide()

	if got, _ := r.drawerRect(r.store.State()); got != half {
		t.Errorf("rect after the un-zoom = %v, want the half %v", got, half)
	}

	r.keys(keyEsc)

	if !r.drawer.closing || r.drawerHasKeys() || r.focus() != before {
		t.Errorf("third esc: closing %v, drawer has the keyboard %v, focus %s, want a close slide with focus back on %s", r.drawer.closing, r.drawerHasKeys(), r.focus(), before)
	}

	r.finishSlide()

	if r.store.State().Tab().Review != nil {
		t.Error("the drawer did not close")
	}
}

func TestReviewBackPage(t *testing.T) {
	t.Parallel()

	m := newPageRig(t, nil)
	m.openPage(t, "README.md")

	m.keys(keyEsc)

	if m.v.page() != nil || m.store.State().Tab().Review == nil || !m.drawerHasKeys() || m.drawer.closing {
		t.Fatalf("esc on a page: page %v, drawer open %v, keyboard %v, closing %v, want the page popped and the drawer kept", m.v.page(), m.store.State().Tab().Review != nil, m.drawerHasKeys(), m.drawer.closing)
	}

	m.keys(keyEsc)
	m.finishSlide()

	if m.store.State().Tab().Review != nil {
		t.Error("the second esc did not close the drawer")
	}
}

func TestReviewBackKeepsPin(t *testing.T) {
	t.Parallel()

	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))

	r.keys(keyEsc)
	r.finishSlide()

	st := r.store.State()
	if st.Tab().Review != nil {
		t.Fatal("esc did not close the drawer")
	}

	if pin := st.Tab().Pin; pin == nil || pin.Root != r.rootB {
		t.Fatalf("Pin = %+v after the close, want b's root %q", pin, r.rootB)
	}

	r.leader('d')
	r.until(t, "the drawer to reopen on b", r.showing(r.rootB))
}

func TestReviewBackDuringSlides(t *testing.T) {
	t.Parallel()

	t.Run("un-zoom slide", func(t *testing.T) {
		t.Parallel()

		r := realZoomRig(t)
		half, _ := r.drawerHalf(r.store.State())

		r.keys(keyRune('z'))
		r.finishSlide()
		r.keys(keyEsc)

		if r.drawer.resizeFrom == 0 {
			t.Fatal("setup: esc started no width slide")
		}

		r.keys(keyEsc)

		if r.drawer.resizeFrom != 0 || !r.drawer.closing {
			t.Fatalf("second esc: resizeFrom %d, closing %v, want a close slide", r.drawer.resizeFrom, r.drawer.closing)
		}

		if full, _ := r.drawerFull(r.store.State()); full != half {
			t.Errorf("sliding out from %v, want the half %v", full, half)
		}

		r.finishSlide()

		if r.store.State().Tab().Review != nil {
			t.Error("the drawer did not close")
		}
	})

	t.Run("zoom slide", func(t *testing.T) {
		t.Parallel()

		r := realZoomRig(t)

		r.keys(keyRune('z'))

		if r.drawer.slideFrom.IsZero() {
			t.Fatal("setup: z started no slide")
		}

		r.keys(keyEsc)

		if r.zoomed() || !r.drawer.slideFrom.IsZero() || !r.drawerHasKeys() {
			t.Errorf("esc during the zoom slide: zoomed %v, slide running %v, keyboard %v, want un-zoomed, no slide, keyboard kept", r.zoomed(), !r.drawer.slideFrom.IsZero(), r.drawerHasKeys())
		}
	})

	t.Run("open slide", func(t *testing.T) {
		t.Parallel()

		r := newKeysRig(t, realKeysRepo(t))
		r.leader('d')
		r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })

		if r.drawer.slideFrom.IsZero() {
			t.Fatal("setup: no slide in is running")
		}

		r.keys(keyEsc)

		if !r.drawer.closing {
			t.Fatal("esc during the open slide started no close slide")
		}

		r.finishSlide()

		if r.store.State().Tab().Review != nil {
			t.Error("the drawer did not close")
		}
	})

	t.Run("close slide", func(t *testing.T) {
		t.Parallel()

		r := realZoomRig(t)
		r.leader('d')

		if !r.drawer.closing {
			t.Fatal("setup: leader d started no close slide")
		}

		r.keys(keyEsc)

		if !r.drawer.closing || r.store.State().Tab().Review == nil {
			t.Errorf("esc during the close slide: closing %v, drawer open %v, want the slide untouched", r.drawer.closing, r.store.State().Tab().Review != nil)
		}
	})
}

func TestReviewBackLeavesListAndChooser(t *testing.T) {
	t.Parallel()

	t.Run("worktree list", func(t *testing.T) {
		t.Parallel()

		r := newFollowRig(t, "")
		r.open(t, r.id)
		r.openList(t)

		r.keys(keyEsc)

		if r.drawer.list != nil || r.store.State().Tab().Review == nil || !r.drawerHasKeys() || r.drawer.closing {
			t.Errorf("esc on the list: list open %v, drawer open %v, keyboard %v, closing %v, want only the list closed", r.drawer.list != nil, r.store.State().Tab().Review != nil, r.drawerHasKeys(), r.drawer.closing)
		}
	})

	t.Run("link chooser", func(t *testing.T) {
		t.Parallel()

		m := newLinkRig(t)
		m.chooser(t)

		m.keys(keyEsc)

		if m.drawer.links != nil || m.store.State().Tab().Review == nil || !m.drawerHasKeys() || m.drawer.closing {
			t.Errorf("esc on the chooser: chooser open %v, drawer open %v, keyboard %v, closing %v, want only the chooser closed", m.drawer.links != nil, m.store.State().Tab().Review != nil, m.drawerHasKeys(), m.drawer.closing)
		}
	})
}

func TestReviewBackRemapped(t *testing.T) {
	t.Parallel()

	r := realZoomRig(t)
	remapTables(t, r.App, drawerKeys("q", "review.back", "esc", "none"))

	r.keys(keyRune('z'))
	r.finishSlide()
	r.keys(keyEsc)

	if !r.zoomed() || r.drawer.closing {
		t.Fatalf("esc after the remap: zoomed %v, closing %v, want nothing changed", r.zoomed(), r.drawer.closing)
	}

	r.keys(keyRune('q'))

	if r.zoomed() || !r.drawerHasKeys() {
		t.Fatalf("q: zoomed %v, keyboard %v, want an un-zoom", r.zoomed(), r.drawerHasKeys())
	}

	r.finishSlide()
	r.keys(keyRune('q'))
	r.finishSlide()

	if r.store.State().Tab().Review != nil {
		t.Error("the second q did not close the drawer")
	}
}
