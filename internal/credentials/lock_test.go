package credentials

import "testing"

func TestLockExclusionAndRelease(t *testing.T) {
	dir := t.TempDir()
	first, err := lockAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockAt(dir); err == nil {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
		t.Error("concurrent session lock acquired")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := lockAt(dir)
	if err != nil {
		t.Fatal("stale lock file blocked a new process")
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}
