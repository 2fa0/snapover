//go:build darwin

package paste

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

func TestForegroundObserverRegistrationAndCleanup(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestForegroundObserverRegistrationAndCleanup$")
	cmd.Env = append(os.Environ(), "SNAPOVER_TEST_FOREGROUND_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native observer: %v\n%s", err, output)
	}
}

// App.Run requires the process's main goroutine, so exercise it in TestMain
// of a child process instead of changing the test runner's application state.
func TestMain(m *testing.M) {
	if os.Getenv("SNAPOVER_TEST_FOREGROUND_CHILD") == "1" {
		if err := checkForegroundObserver(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func checkForegroundObserver() error {
	var calls int
	var watchErr error
	mygo.App.SetName("Snapover Observer Test")
	mygo.App.WhenReady(func() {
		stop, err := WatchForeground(func() { calls++ })
		watchErr = err
		if err == nil {
			ws := objc.ID(objc.GetClass("NSWorkspace")).Send(selSharedWorkspace)
			center := ws.Send(objc.RegisterName("notificationCenter"))
			post := func() {
				center.Send(objc.RegisterName("postNotificationName:object:"),
					nsString("NSWorkspaceDidActivateApplicationNotification"), ws)
			}
			post()
			stop()
			stop()
			post()
		}
		mygo.App.Quit()
	})
	if err := mygo.App.Run(); err != nil {
		return err
	}
	if watchErr != nil || calls != 1 {
		return fmt.Errorf("observer err=%v calls=%d, want exactly one before cleanup", watchErr, calls)
	}
	return nil
}
