package packet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateArtifactsAcceptsNoneAndTaskFiles(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.md")
	nestedDir := filepath.Join(root, "nested")
	if err := os.Mkdir(nestedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(nestedDir, "second.json")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, references := range []string{"none", first, first + "; " + second} {
		value := FromLines([]string{"ARTIFACTS: " + references})
		if err := ValidateArtifacts(value, root); err != nil {
			t.Fatalf("references=%q: %v", references, err)
		}
	}
}

func TestValidateArtifactsRejectsUnsafeReferences(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.md")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "link")
	if err := os.Symlink(inside, symlink); err != nil {
		t.Fatal(err)
	}

	tests := map[string]string{
		"relative":      "inside.md",
		"outside":       outside,
		"missing":       filepath.Join(root, "missing.md"),
		"directory":     directory,
		"symlink":       symlink,
		"not canonical": root + string(filepath.Separator) + "directory" + string(filepath.Separator) + ".." + string(filepath.Separator) + "inside.md",
		"duplicate":     inside + "; " + inside,
	}
	for name, references := range tests {
		t.Run(name, func(t *testing.T) {
			value := FromLines([]string{"ARTIFACTS: " + references})
			if err := ValidateArtifacts(value, root); err == nil || !IsConstraintError(err) {
				t.Fatalf("constraint errorを期待しました: %v", err)
			}
		})
	}
}
