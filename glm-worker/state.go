package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type sessionRole string

const (
	workerRole   sessionRole = "worker"
	reviewerRole sessionRole = "reviewer"
)

type stateStore struct {
	dir string
}

func newStateStore(config appConfig) (*stateStore, error) {
	dir := filepath.Join(config.StateBase, config.RepoHash)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("GLM stateディレクトリを作成できません: %w", err)
	}

	state := &stateStore{dir: dir}
	if err := state.Write("repo-root", config.RepoRoot); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *stateStore) Path(name string) string {
	return filepath.Join(s.dir, name)
}

func (s *stateStore) LockPath() string {
	return s.Path("lock")
}

func (s *stateStore) Exists(name string) bool {
	_, err := os.Stat(s.Path(name))
	return err == nil
}

func (s *stateStore) Read(name string) (string, error) {
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		return "", err
	}
	return stringTrimSpace(data), nil
}

func (s *stateStore) ReadOr(name string, fallback string) string {
	value, err := s.Read(name)
	if err != nil || value == "" {
		return fallback
	}
	return value
}

func (s *stateStore) Write(name string, value string) error {
	if err := writeFileAtomic(s.Path(name), []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("state %sを書き込めません: %w", name, err)
	}
	return nil
}

func (s *stateStore) Touch(name string) error {
	return s.Write(name, "1")
}

func (s *stateStore) Remove(names ...string) error {
	for _, name := range names {
		err := os.Remove(s.Path(name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("state %sを削除できません: %w", name, err)
		}
	}
	return nil
}

func (s *stateStore) SessionID(role sessionRole) (string, bool, error) {
	idName := string(role) + ".id"
	if id, err := s.Read(idName); err == nil && id != "" {
		return id, s.Exists(string(role) + ".ready"), nil
	}

	id, err := newUUID()
	if err != nil {
		return "", false, err
	}
	if err := s.Write(idName, id); err != nil {
		return "", false, err
	}
	return id, false, nil
}

func (s *stateStore) MarkReady(role sessionRole) error {
	return s.Touch(string(role) + ".ready")
}

func (s *stateStore) RemoveUnreadySession(role sessionRole) error {
	if s.Exists(string(role) + ".ready") {
		return nil
	}
	return s.Remove(string(role) + ".id")
}

func printStatus(state *stateStore) error {
	fmt.Printf("REPO: %s\n", state.ReadOr("repo-root", "unknown"))
	fmt.Printf("WORKER_SESSION: %s\n", state.ReadOr("worker.id", "none"))
	fmt.Printf("REVIEWER_SESSION: %s\n", state.ReadOr("reviewer.id", "none"))
	if state.Exists("pending-decision") {
		fmt.Println("PENDING_DECISION: yes")
	} else {
		fmt.Println("PENDING_DECISION: no")
	}
	return nil
}

func resetState(state *stateStore) error {
	names := []string{
		"worker.id",
		"worker.ready",
		"reviewer.id",
		"reviewer.ready",
		"last-request",
		"last-decision",
		"pending-decision",
		"last-review",
		"baseline-status",
		"baseline-worktree.patch",
		"baseline-index.patch",
	}
	if err := state.Remove(names...); err != nil {
		return err
	}

	fmt.Println("STATUS: RESET")
	fmt.Printf("REPO: %s\n", state.ReadOr("repo-root", "unknown"))
	return nil
}
