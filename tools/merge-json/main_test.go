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

func TestMergeFilesCreatesMissingTargetWithPrivateMode(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "nested", "settings.json")
	fragment := filepath.Join(directory, "managed.json")
	if err := os.WriteFile(fragment, []byte(`{"env":{"MANAGED":"yes"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := mergeFiles(target, fragment)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("missing targetの作成を変更として返す必要があります")
	}
	stat, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("target mode = %o", stat.Mode().Perm())
	}
}

func TestMergeFilesPreservesTargetMode(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "settings.json")
	fragment := filepath.Join(directory, "managed.json")
	if err := os.WriteFile(target, []byte(`{"local":true}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragment, []byte(`{"managed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeFiles(target, fragment); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o640 {
		t.Fatalf("target mode = %o", stat.Mode().Perm())
	}
}

func TestMergeFilesRejectsInvalidJSONShapes(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		fragment string
		want     string
	}{
		{name: "target syntax", target: `{broken`, fragment: `{}`, want: "target JSON"},
		{name: "fragment syntax", target: `{}`, fragment: `{broken`, want: "fragment JSON"},
		{name: "target array", target: `[]`, fragment: `{}`, want: "cannot unmarshal array"},
		{name: "fragment array", target: `{}`, fragment: `[]`, want: "cannot unmarshal array"},
		{name: "multiple values", target: `{}`, fragment: `{} {}`, want: "multiple JSON values"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "settings.json")
			fragment := filepath.Join(directory, "managed.json")
			if err := os.WriteFile(target, []byte(test.target), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fragment, []byte(test.fragment), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := mergeFiles(target, fragment)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
