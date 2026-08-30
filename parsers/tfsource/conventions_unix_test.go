//go:build unix

package tfsource

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// os.ReadFile on a named pipe waits for a writer that may never come, and a
// scan that hangs with no output looks like a scan that is working. The test
// has a deadline because the failure it guards against is not returning.
func TestAPathThatIsNotAFileIsRefusedRatherThanRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot make a named pipe here: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Read(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a named pipe was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read did not come back: it is waiting for a writer")
	}
}
