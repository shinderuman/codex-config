package packet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func artifactRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	inside := filepath.Join(root, "inside.md")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, inside
}

func TestValidateArtifactsAcceptsNoneAndTaskFiles(t *testing.T) {
	root, inside := artifactRoot(t)
	nestedDir := filepath.Join(root, "nested")
	if err := os.Mkdir(nestedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(nestedDir, "second.json")
	if err := os.WriteFile(second, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := [][]string{
		nil,
		{},
		{inside},
		{inside, second},
	}
	for _, artifacts := range cases {
		if err := ValidateArtifacts(artifacts, root); err != nil {
			t.Fatalf("artifacts=%v: %v", artifacts, err)
		}
	}
}

func TestValidateArtifactsRejectsUnsafeReferences(t *testing.T) {
	root, inside := artifactRoot(t)
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

	tests := map[string][]string{
		"empty":         {""},
		"relative":      {"inside.md"},
		"outside":       {outside},
		"missing":       {filepath.Join(root, "missing.md")},
		"directory":     {directory},
		"symlink":       {symlink},
		"not canonical": {root + string(filepath.Separator) + "directory" + string(filepath.Separator) + ".." + string(filepath.Separator) + "inside.md"},
		"duplicate":     {inside, inside},
	}
	for name, artifacts := range tests {
		t.Run(name, func(t *testing.T) {
			err := ValidateArtifacts(artifacts, root)
			if err == nil || !IsConstraintError(err) {
				t.Fatalf("constraint errorを期待しました: %v", err)
			}
		})
	}
}

func TestValidateArtifactsRejectsMultilineEntry(t *testing.T) {
	result := implementedResult()
	result.Artifacts = []string{"/tmp/a\n.md"}
	err := ValidateWorkerResult(result)
	if err == nil || !strings.Contains(err.Error(), "改行") {
		t.Fatalf("err = %v", err)
	}
}
