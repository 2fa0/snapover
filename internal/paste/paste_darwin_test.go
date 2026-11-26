//go:build darwin

package paste

import (
	"errors"
	"testing"
	"time"
)

func TestCaptureAndKeyEventDoNotPanic(t *testing.T) {
	_ = Capture()
	if !Allowed() && axErr != nil {
		t.Fatal(axErr)
	}
	loadCG()
	if cgErr != nil {
		t.Fatal(cgErr)
	}
	ev := cgCreate(0, kVK_ANSI_V, 1)
	if ev == 0 {
		t.Fatal("keyboard event was not created")
	}
	cfRelease(ev)
}

func TestTextInputRoleAndCFString(t *testing.T) {
	for _, role := range []string{"AXTextField", "AXTextArea", "AXComboBox", "AXSearchField"} {
		if !isTextInputRole(role) {
			t.Errorf("isTextInputRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"", "AXScrollArea", "AXGroup", "AXOutline", "AXWindow"} {
		if isTextInputRole(role) {
			t.Errorf("isTextInputRole(%q) = true, want false", role)
		}
	}
	loadCF()
	if got, ok := cfString(uintptr(nsString("AXTextField"))); !ok || got != "AXTextField" {
		t.Fatalf("cfString = %q, %v; want AXTextField, true", got, ok)
	}
	if got, ok := cfString(0); ok || got != "" {
		t.Fatalf("cfString(0) = %q, %v; want empty, false", got, ok)
	}
	if err := (Target{}).CheckPasteTarget(false); err != nil {
		t.Fatalf("invalid target should be left to SendPaste, got %v", err)
	}
}

func TestFinderFocusQuery(t *testing.T) {
	for _, tc := range []struct {
		name, role  string
		focusStatus int32
		failTimeout bool
		wantQueries int
		textInput   bool
		wantErr     error
	}{
		{name: "text field", role: "AXTextField", wantQueries: 2, textInput: true},
		{name: "file list", role: "AXOutline", wantQueries: 2},
		{name: "no focused element", focusStatus: axErrNoValue, wantQueries: 1},
		{name: "focus query failed", focusStatus: -25204, wantQueries: 1, wantErr: ErrUnknownFocus},
		{name: "could not set element timeout", failTimeout: true, wantQueries: 1, wantErr: ErrUnknownFocus},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockCaretAX(t)
			var budgets []float32
			axSetTimeout = func(element uintptr, seconds float32) int32 {
				budgets = append(budgets, seconds)
				if element == 2 && tc.failTimeout {
					return -1
				}
				return 0
			}
			calls := 0
			axCopyAttribute = func(element, _ uintptr, value *uintptr) int32 {
				calls++
				if element == 1 {
					time.Sleep(time.Millisecond)
					*value = 2
					return tc.focusStatus
				}
				*value = uintptr(nsString(tc.role))
				return 0
			}
			got, err := hasFocusedTextInput(123)
			if got != tc.textInput || !errors.Is(err, tc.wantErr) || calls != tc.wantQueries {
				t.Fatalf("focused input = %v, err = %v, queries = %d", got, err, calls)
			}
			if len(budgets) == 2 && (budgets[0] > float32(caretTimeout.Seconds()) || budgets[1] >= budgets[0]) {
				t.Fatalf("queries did not share the timeout: %v", budgets)
			}
		})
	}
}
