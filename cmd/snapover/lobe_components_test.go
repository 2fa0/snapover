package main

import (
	"context"
	"math"
	"testing"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/history"
	"github.com/2fa0/snapover/internal/settings"
	"github.com/egoist/mygo/ui"
)

func TestSearchClearRestoresResultsAndFocus(t *testing.T) {
	for _, picker := range []bool{false, true} {
		for _, lang := range []string{settings.LangZH, settings.LangEN} {
			a := interfaceTestApp(t)
			a.cfg.Values.Language = lang
			for _, text := range []string{"alpha", "beta"} {
				item, _ := clip.FromText(text)
				if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
					t.Fatal(err)
				}
			}
			a.refreshEntries()
			a.page = pageHistory
			view := a.view
			query, selected, search := &a.query, &a.histSel, &a.historySearch
			label := a.tr("搜索复制历史")
			if picker {
				view = a.pickerView
				query, selected, search = &a.pickQuery, &a.pickSel, &a.pickerSearch
				label = a.tr("搜索快速历史")
			}
			*query = "alpha"
			tt := ui.NewTester(view, 960, 640)
			*selected = 0
			ctx, cancel := context.WithCancel(context.Background())
			search.cancel, search.pending = cancel, make(chan searchResult, 1)
			tt.Frame()
			if picker && !tt.Focused(label) {
				t.Fatal("loading indicator took the focus from the search field")
			}
			clickInterface(t, tt, a.tr("清空搜索"))
			if *query != "" || search.pending != nil || ctx.Err() == nil {
				t.Fatalf("picker=%v lang=%s: query=%q pending=%v canceled=%v", picker, lang, *query, search.pending != nil, ctx.Err())
			}
			wantSelection := -1
			if picker {
				wantSelection = 0
			}
			if *selected != wantSelection || !tt.Focused(label) {
				t.Fatalf("picker=%v: selection=%d, focus=%v", picker, *selected, tt.Focused(label))
			}
			if !tt.HasText("alpha") || !tt.HasText("beta") {
				t.Fatal("clearing did not restore the unfiltered list")
			}
			tt.Type("beta")
			if *query != "beta" {
				t.Fatal("typing after clearing did not reach the search field")
			}
		}
	}
}

func TestNumberInputStepsDraftWithoutSavingOrClampingInvalidText(t *testing.T) {
	a := &app{}
	value, saved := "2", "2"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			if a.numberInput(c, &value, "数值", 1, 3, 100).Submitted() {
				saved = value
			}
			ui.Button(c, "其他设置")
		})
	}, 320, 180)
	clickInterface(t, tt, "增加 数值")
	if value != "3" || saved != "2" || !tt.Focused("数值") {
		t.Fatal("step button should update only the draft and return focus")
	}
	tt.Key(0, ui.KeyUp)
	if value != "3" {
		t.Fatal("Up exceeded the upper bound")
	}
	tt.Key(0, ui.KeyDown)
	if value != "2" {
		t.Fatal("Down did not decrement the draft")
	}
	clickInterface(t, tt, "其他设置")
	if saved != "2" {
		t.Fatal("losing focus saved the draft")
	}
	clickInterface(t, tt, "数值")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("25")
	tt.Key(0, ui.KeyUp)
	clickInterface(t, tt, "其他设置")
	if value != "25" {
		t.Fatal("invalid typed value was silently clamped")
	}
	clickInterface(t, tt, "数值")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("3")
	tt.Key(0, ui.KeyEnter)
	if saved != "3" {
		t.Fatal("Enter no longer submits the input")
	}
}

func TestNumberInputKeepsExactIntegersAndEmptyRanges(t *testing.T) {
	a := &app{}
	value := ""
	tt := ui.NewTester(func(c *ui.Context) {
		a.numberInput(c, &value, "序号", 1, math.MaxInt64, 220)
	}, 320, 120)
	if value != "" {
		t.Fatal("an empty backup range was changed on display")
	}
	clickInterface(t, tt, "增加 序号")
	if value != "1" {
		t.Fatal("step did not initialize an empty draft")
	}
	tt.Key(0, ui.KeyDown)
	if value != "1" {
		t.Fatal("Down exceeded the lower bound")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("9223372036854775807")
	tt.Key(0, ui.KeyUp)
	if value != "9223372036854775807" {
		t.Fatal("integer overflow or precision loss at the upper bound")
	}
	tt.Key(0, ui.KeyDown)
	if value != "9223372036854775806" {
		t.Fatal("large integer step lost precision")
	}
}

func TestEmptySearchActionRestoresHistoryAndTyping(t *testing.T) {
	for _, picker := range []bool{false, true} {
		a := interfaceTestApp(t)
		item, _ := clip.FromText("alpha")
		if _, err := a.hist.Add(history.DirLocal, "", item); err != nil {
			t.Fatal(err)
		}
		a.refreshEntries()
		a.page = pageHistory
		view, query, label := a.view, &a.query, "搜索复制历史"
		if picker {
			view, query, label = a.pickerView, &a.pickQuery, "搜索快速历史"
		}
		*query = "missing"
		tt := ui.NewTesterWith(view, 960, 640, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
		clickInterface(t, tt, "清空搜索并查看全部记录")
		if *query != "" || !tt.HasText("alpha") || !tt.Focused(label) {
			t.Fatalf("picker=%v: empty-state action did not restore results and focus", picker)
		}
		tt.Type("alpha")
		if *query != "alpha" {
			t.Fatal("typing after the empty-state action missed the search field")
		}
	}
}
