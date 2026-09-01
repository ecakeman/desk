package worker

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDoneReapsChild(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "w.py")
	if err := os.WriteFile(script, []byte(`
import os, sys, time, json
open(r"`+pidFile+`", "w").write(str(os.getpid()))
sys.stdin.readline()
sys.stdout.write(json.dumps({"t":"turn.finish","text":"ok"}) + "\n")
sys.stdout.flush()
time.sleep(30)
`), 0o644); err != nil {
		t.Fatal(err)
	}
	proc := NewProcess("python3", script, append(os.Environ(), "PYTHONUNBUFFERED=1"))
	errCh := make(chan error, 1)
	go func() {
		_, err := proc.Handle(In{T: "turn.start", RunID: "r1"}, nil)
		errCh <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(pidFile)
		if err == nil {
			n, conv := strconv.Atoi(strings.TrimSpace(string(b)))
			if conv == nil && n > 1 {
				pid = n
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("child pid not written")
	}
	proc.Done("r1")
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Handle did not return after Done")
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive", pid)
}
