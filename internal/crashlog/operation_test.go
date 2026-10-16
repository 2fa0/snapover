package crashlog

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestOperationRecordsBlockedStackAndRecovery(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)
	path, err := Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	finish := trackOperation("测试剪贴板操作", 0, 10*time.Millisecond)
	t.Cleanup(func() {
		if finish != nil {
			finish()
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		buf, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(buf)
		if strings.Contains(text, "操作长时间未返回 name=测试剪贴板操作") {
			if !strings.Contains(text, "TestOperationRecordsBlockedStackAndRecovery") {
				t.Fatal("dump did not include the blocked caller's Go stack")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("blocked operation did not record a stack")
		}
		time.Sleep(5 * time.Millisecond)
	}
	finish()
	finish = nil
	writef("")
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf), "慢操作结束 name=测试剪贴板操作 elapsed=") {
		t.Fatal("slow operation recovery was not recorded")
	}
}

func TestFastOperationStopsDiagnostics(t *testing.T) {
	resetForTest()
	t.Cleanup(resetForTest)
	path, err := Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	trackOperation("fast", time.Hour, 10*time.Millisecond)()
	time.Sleep(30 * time.Millisecond)
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(buf), "name=fast") {
		t.Fatal("fast operation added diagnostic noise")
	}
}
