package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const artifactDirectoryName = "artifacts"

func (s *StateStore) ArtifactDir(taskID string) string {
	return s.Path(filepath.Join(artifactDirectoryName, taskID))
}

func (s *StateStore) PrepareArtifactDir() (string, error) {
	taskID, err := s.TaskID()
	if err != nil {
		return "", err
	}
	if err := validateArtifactTaskID(taskID); err != nil {
		return "", err
	}

	dir := s.ArtifactDir(taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("artifactディレクトリを作成できません: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("artifactディレクトリの権限を設定できません: %w", err)
	}
	return dir, nil
}

// SecureArtifactDirはモデルが作成した現在タスクの成果物を所有者限定へ揃える。
// state外を指すsymlinkや特殊ファイルは成果物として認めない。
func (s *StateStore) SecureArtifactDir() error {
	taskID, err := s.TaskID()
	if err != nil {
		return err
	}
	if err := validateArtifactTaskID(taskID); err != nil {
		return err
	}
	root := s.ArtifactDir(taskID)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinkはartifactに保存できません: %s", path)
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("通常ファイル以外はartifactに保存できません: %s", path)
		}
		return os.Chmod(path, 0o600)
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("artifactの権限を保護できません: %w", err)
	}
	return nil
}

func validateArtifactTaskID(taskID string) error {
	if taskID == "" || taskID == "." || taskID == ".." || filepath.IsAbs(taskID) || strings.ContainsAny(taskID, `/\`) {
		return fmt.Errorf("artifact用task IDが不正です: %q", taskID)
	}
	return nil
}
