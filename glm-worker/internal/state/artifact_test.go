package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/config"
)

func TestArtifactDirIsTaskScopedAndSecured(t *testing.T) {
	store, err := NewStateStore(config.AppConfig{
		StateBase: t.TempDir(),
		RepoHash:  "artifact-test",
		RepoRoot:  "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := store.StartNewTask()
	if err != nil {
		t.Fatal(err)
	}

	dir, err := store.PrepareArtifactDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != store.ArtifactDir(taskID) {
		t.Fatalf("artifact dir = %q", dir)
	}
	file := filepath.Join(dir, "report.md")
	if err := os.WriteFile(file, []byte("report"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SecureArtifactDir(); err != nil {
		t.Fatal(err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("artifact dir mode = %o", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("artifact file mode = %o", got)
	}
}

func TestArtifactDirRejectsSymlink(t *testing.T) {
	store, err := NewStateStore(config.AppConfig{
		StateBase: t.TempDir(),
		RepoHash:  "artifact-symlink-test",
		RepoRoot:  "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartNewTask(); err != nil {
		t.Fatal(err)
	}
	dir, err := store.PrepareArtifactDir()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := store.SecureArtifactDir(); err == nil {
		t.Fatal("symlinkを拒否しませんでした")
	}
}

func TestArtifactDirRejectsPathTraversalTaskID(t *testing.T) {
	store, err := NewStateStore(config.AppConfig{
		StateBase: t.TempDir(),
		RepoHash:  "artifact-traversal-test",
		RepoRoot:  "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write("task.id", "../../outside"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareArtifactDir(); err == nil {
		t.Fatal("path traversalを含むtask IDを拒否しませんでした")
	}
}
