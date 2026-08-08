package main

import (
	"path/filepath"
	"testing"
)

func TestSessionIDPersists(t *testing.T) {
	state := &stateStore{dir: t.TempDir()}

	first, ready, err := state.SessionID(workerRole)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("new session should not be ready")
	}

	if err := state.MarkReady(workerRole); err != nil {
		t.Fatal(err)
	}

	second, ready, err := state.SessionID(workerRole)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("session should be ready")
	}
	if first != second {
		t.Fatalf("session changed: %s -> %s", first, second)
	}

	if filepath.Base(state.Path("worker.id")) != "worker.id" {
		t.Fatal("unexpected state path")
	}
}
