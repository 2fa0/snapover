package keepawake

import (
	"errors"
	"fmt"
	"testing"
)

func TestActiveSessionFallback(t *testing.T) {
	denied := errors.New("access denied")
	for _, test := range []struct {
		name         string
		queryActive  bool
		queryErr     error
		console      bool
		consoleErr   error
		wantFallback bool
		wantActive   bool
		wantErr      error
	}{
		{name: "active WTS session", queryActive: true, wantActive: true},
		{name: "disconnected WTS session", console: true},
		{name: "service unavailable, local console", queryErr: fmt.Errorf("query: %w", errSessionUnavailable), console: true, wantFallback: true, wantActive: true},
		{name: "service unavailable, other session", queryErr: errSessionUnavailable, wantFallback: true},
		{name: "service unavailable, console check fails", queryErr: errSessionUnavailable, consoleErr: denied, wantFallback: true, wantErr: denied},
		{name: "access denied must not fall back", queryErr: denied, console: true, wantErr: denied},
	} {
		t.Run(test.name, func(t *testing.T) {
			fallback := false
			active, err := activeSession(func() (bool, error) {
				return test.queryActive, test.queryErr
			}, func() (bool, error) {
				fallback = true
				return test.console, test.consoleErr
			})
			if active != test.wantActive || !errors.Is(err, test.wantErr) || fallback != test.wantFallback {
				t.Fatalf("active=%v err=%v fallback=%v", active, err, fallback)
			}
		})
	}
}
