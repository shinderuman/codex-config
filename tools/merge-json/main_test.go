package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeFilesPreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")

	if err := os.WriteFile(target, []byte(`{"permissions":{"allow":["x"]},"env":{"LOCAL":"keep"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragment, []byte(`{"env":{"MANAGED":"yes"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := mergeFiles(target, fragment)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	for _, want := range []string{`"permissions"`, `"LOCAL": "keep"`, `"MANAGED": "yes"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestMergeFilesIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")

	if err := os.WriteFile(target, []byte(`{"env":{"A":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragment, []byte(`{"env":{"A":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := mergeFiles(target, fragment)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected unchanged")
	}
}
