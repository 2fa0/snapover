package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
)

func TestPickerKeyboardSelection(t *testing.T) {
	a := &app{pickSel: 0}
	a.pickList.Selected = &a.pickSel
	for i := range 30 {
		a.entries = append(a.entries, history.Entry{ID: fmt.Sprint(i), Kind: string(clip.KindText), Text: fmt.Sprintf("item %02d", i)})
	}
	tt := ui.NewTester(a.pickerView, 380, 440)
	if _, active := tt.TextCaret(); !active {
		t.Fatal("search field should initially have focus")
	}
	tt.Key(0, ui.KeyDown)
	if a.pickSel != 1 {
		t.Fatalf("Down selected %d, want 1", a.pickSel)
	}
	tt.Key(0, ui.KeyUp)
	if a.pickSel != 0 {
		t.Fatalf("Up selected %d, want 0", a.pickSel)
	}
	tt.Key(0, ui.KeyUp)
	if a.pickSel != 29 {
		t.Fatalf("Up should wrap to last item, got %d", a.pickSel)
	}
	first, last := a.pickList.Visible()
	if first > a.pickSel || last < a.pickSel {
		t.Fatalf("selected row %d is outside visible rows %d..%d", a.pickSel, first, last)
	}
	tt.Key(0, ui.KeyDown)
	if a.pickSel != 0 {
		t.Fatalf("Down should wrap to first item, got %d", a.pickSel)
	}
	tt.Type("item 12")
	if a.pickSel != 0 || len(a.filtered(a.pickQuery)) != 1 {
		t.Fatalf("search did not reset selection: query=%q selection=%d", a.pickQuery, a.pickSel)
	}
	tt.Type("missing")
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyUp)
	if a.pickSel != -1 {
		t.Fatalf("empty search results selected row %d", a.pickSel)
	}
}

func TestPickerBounds(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 850}
	for _, tc := range []struct {
		name   string
		anchor mygo.Rectangle
		area   mygo.Rectangle
		want   mygo.Rectangle
	}{
		{"below caret", mygo.Rectangle{X: 200, Y: 200, Height: 20}, area, mygo.Rectangle{X: 200, Y: 224, Width: 380, Height: 440}},
		{"above caret near bottom", mygo.Rectangle{X: 200, Y: 800, Height: 20}, area, mygo.Rectangle{X: 200, Y: 356, Width: 380, Height: 440}},
		{"right screen edge", mygo.Rectangle{X: 1400, Y: 200, Height: 20}, area, mygo.Rectangle{X: 1060, Y: 224, Width: 380, Height: 440}},
		{"display left of primary", mygo.Rectangle{X: -1500, Y: 200, Height: 20}, mygo.Rectangle{X: -1920, Y: 25, Width: 1920, Height: 1055}, mygo.Rectangle{X: -1500, Y: 224, Width: 380, Height: 440}},
		{"small work area", mygo.Rectangle{X: 100, Y: 100, Height: 20}, mygo.Rectangle{X: 0, Y: 25, Width: 320, Height: 400}, mygo.Rectangle{X: 0, Y: 25, Width: 320, Height: 400}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickerBounds(tc.anchor, tc.area); got != tc.want {
				t.Fatalf("bounds = %+v, want %+v", got, tc.want)
			}
		})
	}
}
