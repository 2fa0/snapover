package discovery

import "testing"

func TestSelectionNarrowsWithoutAdoptingFutureSegments(t *testing.T) {
	wifi := "en0/192.168.1.0/24"
	lan := "en1/10.0.0.0/24"
	current := []string{wifi, lan}
	all := Remembered("", nil, nil)
	if !all.All() || !all.Scope().Allows("anything") || all.HasNew(current) {
		t.Fatalf("unset selection should keep every segment: %+v", all)
	}

	narrowed := Toggle(all, current, lan, false)
	if narrowed.All() || narrowed.Stopped() || narrowed.Scope().Allows(lan) || !narrowed.Scope().Allows(wifi) {
		t.Fatalf("uncheck did not leave only %s: %+v", wifi, narrowed)
	}
	if narrowed.HasNew(current) {
		t.Fatal("segments visible at the edit were treated as new")
	}
	dock := "en2/172.16.0.0/16"
	if !narrowed.HasNew([]string{wifi, lan, dock}) {
		t.Fatal("a segment attached after the edit was not called out")
	}

	checked := Toggle(narrowed, []string{wifi, lan, dock}, dock, true)
	if checked.All() || !checked.Scope().Allows(dock) || checked.HasNew([]string{wifi, lan, dock}) {
		t.Fatalf("checking a new segment adopted future segments or kept the hint: %+v", checked)
	}
	restored := Toggle(narrowed, current, lan, true)
	if restored.All() {
		t.Fatal("checking every current segment must not include segments that appear later")
	}

	none := SelectNone(current)
	if !none.Stopped() || none.Scope().Allows(wifi) || none.HasNew(current) {
		t.Fatalf("clearing the selection did not stop discovery: %+v", none)
	}
	again := SelectAll()
	if !again.All() || len(again.Nets) != 0 || len(again.Seen) != 0 {
		t.Fatalf("select all did not clear the explicit list: %+v", again)
	}
}

func TestRememberedIgnoresUnknownMode(t *testing.T) {
	got := Remembered("all", []string{"en0/192.168.1.0/24"}, nil)
	if !got.All() || len(got.Nets) != 0 {
		t.Fatalf("unknown mode kept an allowlist: %+v", got)
	}
	explicit := Remembered(modeSelected, []string{"", "en1/10.0.0.0/24", "en1/10.0.0.0/24"}, nil)
	if explicit.All() || len(explicit.Nets) != 1 {
		t.Fatalf("explicit list was not normalized: %+v", explicit)
	}
}
