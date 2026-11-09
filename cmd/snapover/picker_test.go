package main

import (
	"fmt"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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

func TestHistoryMatchMarksEveryMatchOfTheSnippet(t *testing.T) {
	entry := history.SearchPreview(history.Entry{Text: "2024 年 3 月 5 日：2024 的重复 2024"}, "2024")
	base, mark := ui.RGBA(1, 2, 3, 1), ui.RGBA(10, 20, 30, 1)
	spans := historyMatchSpans(entry, matchMark{plain: base, match: mark, block: mark})
	var text string
	marked := 0
	for _, span := range spans {
		text += span.Text
		if span.Background != mark {
			continue
		}
		marked++
		if span.Text != "2024" || span.Weight != 700 || span.Radius == 0 || span.Color != mark {
			t.Fatalf("mark %d = %+v, want the query in bold on a rounded block", marked, span)
		}
	}
	if text != entry.MatchText {
		t.Fatalf("spans spell %q, want %q", text, entry.MatchText)
	}
	if marked != 3 {
		t.Fatalf("marked %d matches, want every 3 of %q", marked, entry.MatchText)
	}
}

func TestHistoryMatchSpansKeepFieldNameAndTolerateStaleRanges(t *testing.T) {
	entry := history.Entry{
		MatchField:  "来源应用",
		MatchText:   "Visual Studio Code",
		MatchRanges: []history.MatchRange{{Start: 7, End: 13}, {Start: 2, End: 4}, {Start: 14, End: 99}},
	}
	spans := historyMatchSpans(entry, matchMark{plain: ui.RGBA(1, 2, 3, 1), match: ui.RGBA(10, 20, 30, 1), block: ui.RGBA(10, 20, 30, 1)})
	if spans[0].Text != "来源应用：" {
		t.Fatalf("field prefix = %q", spans[0].Text)
	}
	var text string
	for _, span := range spans {
		text += span.Text
	}
	if text != entry.MatchField+"："+entry.MatchText {
		t.Fatalf("spans spell %q", text)
	}
	// Only the first range is in order and inside the text; the others would
	// repeat or split it, so they are dropped rather than drawn.
	marked := 0
	for _, span := range spans[1:] {
		if span.Background != (ui.Color{}) {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("marked %d runs, want 1", marked)
	}
}

func TestPickerBounds(t *testing.T) {
	area := mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 850}
	for _, tc := range []struct {
		name          string
		anchor        mygo.Rectangle
		area          mygo.Rectangle
		width, height int
		want          mygo.Rectangle
	}{
		{"below pointer", mygo.Rectangle{X: 200, Y: 200}, area, 380, 440, mygo.Rectangle{X: 10, Y: 204, Width: 380, Height: 440}},
		{"above pointer near bottom", mygo.Rectangle{X: 200, Y: 800}, area, 380, 440, mygo.Rectangle{X: 10, Y: 356, Width: 380, Height: 440}},
		{"right screen edge", mygo.Rectangle{X: 1400, Y: 200}, area, 380, 440, mygo.Rectangle{X: 1060, Y: 204, Width: 380, Height: 440}},
		{"left screen edge", mygo.Rectangle{X: 50, Y: 200}, area, 380, 440, mygo.Rectangle{X: 0, Y: 204, Width: 380, Height: 440}},
		{"display left of primary", mygo.Rectangle{X: -1500, Y: 200}, mygo.Rectangle{X: -1920, Y: 25, Width: 1920, Height: 1055}, 380, 440, mygo.Rectangle{X: -1690, Y: 204, Width: 380, Height: 440}},
		{"display above primary", mygo.Rectangle{X: 200, Y: -800}, mygo.Rectangle{X: 0, Y: -1080, Width: 1920, Height: 1040}, 380, 440, mygo.Rectangle{X: 10, Y: -796, Width: 380, Height: 440}},
		{"small work area", mygo.Rectangle{X: 100, Y: 100}, mygo.Rectangle{X: 0, Y: 25, Width: 320, Height: 400}, 380, 440, mygo.Rectangle{X: 0, Y: 25, Width: 320, Height: 400}},
		// A custom size keeps the same anchoring rules as the default one.
		{"custom size", mygo.Rectangle{X: 200, Y: 200}, area, 600, 700, mygo.Rectangle{X: 0, Y: 25, Width: 600, Height: 700}},
		{"custom size above pointer", mygo.Rectangle{X: 200, Y: 760}, area, 600, 700, mygo.Rectangle{X: 0, Y: 56, Width: 600, Height: 700}},
		{"custom size larger than work area", mygo.Rectangle{X: 100, Y: 100}, mygo.Rectangle{X: 0, Y: 25, Width: 320, Height: 400}, 600, 700, mygo.Rectangle{X: 0, Y: 25, Width: 320, Height: 400}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickerBounds(tc.anchor, tc.area, tc.width, tc.height); got != tc.want {
				t.Fatalf("bounds = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTrustworthyPickerCaret(t *testing.T) {
	for _, tc := range []struct {
		name string
		rect mygo.Rectangle
		want bool
	}{
		{"caret", mygo.Rectangle{Height: 20}, true},
		{"text field", mygo.Rectangle{Width: 240, Height: 24}, true},
		{"empty", mygo.Rectangle{}, false},
		{"editor", mygo.Rectangle{Width: 800, Height: 600}, false},
		{"too tall", mygo.Rectangle{Width: 8, Height: 120}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trustworthyPickerCaret(tc.rect); got != tc.want {
				t.Fatalf("trustworthy(%+v) = %v, want %v", tc.rect, got, tc.want)
			}
		})
	}
}

func TestPlaceShortcutPicker(t *testing.T) {
	work := mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 850}
	other := mygo.Rectangle{X: -1920, Y: 25, Width: 1920, Height: 1055}
	mouseOnWork := mygo.Rectangle{X: 200, Y: 200}
	mouseOffWork := mygo.Rectangle{X: -1500, Y: 200}
	caret := mygo.Rectangle{X: 240, Y: 180, Height: 20}
	huge := mygo.Rectangle{X: 100, Y: 80, Width: 900, Height: 640}
	window := mygo.Rectangle{X: 80, Y: 40, Width: 900, Height: 700}
	for _, tc := range []struct {
		name string
		in   pickerPlaceInput
		want mygo.Rectangle
	}{
		{
			name: "pointer on working display",
			in:   pickerPlaceInput{width: 380, height: 440, mouse: mouseOnWork, mouseArea: work, mouseOnWork: true, caret: caret, hasCaret: true, window: window, hasWindow: true, workArea: work},
			want: pickerBounds(mouseOnWork, work, 380, 440),
		},
		{
			name: "pointer elsewhere uses caret",
			in:   pickerPlaceInput{width: 380, height: 440, mouse: mouseOffWork, mouseArea: other, mouseOnWork: false, caret: caret, hasCaret: true, window: window, hasWindow: true, workArea: work},
			want: pickerBounds(caret, work, 380, 440),
		},
		{
			name: "pointer elsewhere uses window when caret is an editor",
			in:   pickerPlaceInput{width: 380, height: 440, mouse: mouseOffWork, mouseArea: other, mouseOnWork: false, caret: huge, hasCaret: true, window: window, hasWindow: true, workArea: work},
			want: pickerCenteredIn(window, work, 380, 440),
		},
		{
			name: "pointer elsewhere uses window without caret",
			in:   pickerPlaceInput{width: 380, height: 440, mouse: mouseOffWork, mouseArea: other, mouseOnWork: false, window: window, hasWindow: true, workArea: work},
			want: pickerCenteredIn(window, work, 380, 440),
		},
		{
			name: "no working window falls back to pointer",
			in:   pickerPlaceInput{width: 380, height: 440, mouse: mouseOffWork, mouseArea: other, mouseOnWork: false, workArea: work},
			want: pickerBounds(mouseOffWork, other, 380, 440),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := placeShortcutPicker(tc.in); got != tc.want {
				t.Fatalf("place = %+v, want %+v", got, tc.want)
			}
		})
	}
}
