// Package stateはリポジトリ別のstateディレクトリ上でタスク状態・session・
// resume checkpoint・観測用stats mirror・git baselineを管理する。
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shinderuman/codex-config/glm-worker/internal/config"
	"github.com/shinderuman/codex-config/glm-worker/internal/packet"
)

// SessionRoleはworker/reviewerの役割を区別する。
type SessionRole string

// TaskStatusはタスクの状態遷移を表す。
type TaskStatus string

const (
	WorkerRole   SessionRole = "worker"
	ReviewerRole SessionRole = "reviewer"

	TaskStatusActive           TaskStatus = "active"
	TaskStatusWaitingDecision  TaskStatus = "waiting-decision"
	TaskStatusWaitingSolReview TaskStatus = "waiting-sol-review"
	TaskStatusComplete         TaskStatus = "complete"
	TaskStatusRateLimited      TaskStatus = "rate-limited"
)

// StateStoreはリポジトリ別stateディレクトリへのアクセスを提供する。
type StateStore struct {
	dir string
}

// NewStateStoreはconfigからstateディレクトリを構築し初期化する。
func NewStateStore(config config.AppConfig) (*StateStore, error) {
	dir := filepath.Join(config.StateBase, config.RepoHash)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("GLM stateディレクトリを作成できません: %w", err)
	}

	state := &StateStore{dir: dir}
	if err := state.Write("repo-root", config.RepoRoot); err != nil {
		return nil, err
	}
	return state, nil
}

// Pathはstate配下のファイルパスを返す。
func (s *StateStore) Path(name string) string {
	return filepath.Join(s.dir, name)
}

// LockPathはプロセス間ロック用ファイルパスを返す。
func (s *StateStore) LockPath() string {
	return s.Path("lock")
}

// Existsはstateファイルが存在するかを返す。
func (s *StateStore) Exists(name string) bool {
	_, err := os.Stat(s.Path(name))
	return err == nil
}

// Readはstateファイルを読み込み前後の空白を除去して返す。
func (s *StateStore) Read(name string) (string, error) {
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// ReadOrは読み込み失敗または空時にfallbackを返す。
func (s *StateStore) ReadOr(name string, fallback string) string {
	value, err := s.Read(name)
	if err != nil || value == "" {
		return fallback
	}
	return value
}

// Writeは値を末尾改行付きで原子的に書き込む。
func (s *StateStore) Write(name string, value string) error {
	if err := writeFileAtomic(s.Path(name), []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("state %sを書き込めません: %w", name, err)
	}
	return nil
}

// Touchは存在マーカーとして"1"を書き込む。
func (s *StateStore) Touch(name string) error {
	return s.Write(name, "1")
}

// Removeは指定stateファイルを存在しなければ何もしないで削除する。
func (s *StateStore) Remove(names ...string) error {
	for _, name := range names {
		err := os.Remove(s.Path(name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("state %sを削除できません: %w", name, err)
		}
	}
	return nil
}

// StartNewTaskは現在タスクをアーカイブし新規タスク状態へ初期化する。
func (s *StateStore) StartNewTask() (string, error) {
	s.ArchiveCurrentStats()
	if err := s.Remove(
		"task.id",
		"worker.id",
		"worker.ready",
		"reviewer.id",
		"reviewer.ready",
		"task.status",
	); err != nil {
		return "", err
	}

	taskID, err := newUUID()
	if err != nil {
		return "", err
	}
	if err := s.Write("task.id", taskID); err != nil {
		return "", err
	}
	s.InitializeTaskStats(taskID)
	if err := s.SetTaskStatus(TaskStatusActive); err != nil {
		return "", err
	}
	return taskID, nil
}

// TaskIDは現在タスクのIDを返す。未設定時は"legacy"。
func (s *StateStore) TaskID() string {
	return s.ReadOr("task.id", "legacy")
}

// TaskStatusは現在タスクの状態を、各種stateファイルから推論して返す。
func (s *StateStore) TaskStatus() TaskStatus {
	if status, err := s.Read("task.status"); err == nil && status != "" {
		return TaskStatus(status)
	}
	if s.Exists("pending-decision") {
		return TaskStatusWaitingDecision
	}
	if checkpoint, err := s.LoadResumeCheckpoint(); err == nil && checkpoint.RateLimited {
		return TaskStatusRateLimited
	}
	if review, err := s.Read("last-review"); err == nil {
		switch packet.FromLines(strings.Split(review, "\n")).Status() {
		case "PASS":
			return TaskStatusComplete
		case "NEEDS_SOL_REVIEW":
			return TaskStatusWaitingSolReview
		}
	}
	if s.Exists("task.id") {
		return TaskStatusActive
	}
	return TaskStatus("none")
}

// SetTaskStatusはtask.statusを書き込みstats mirrorへも反映する。
func (s *StateStore) SetTaskStatus(status TaskStatus) error {
	if err := s.Write("task.status", string(status)); err != nil {
		return err
	}
	s.UpdateTaskStats(func(stats *TaskStats) {
		stats.Status = status
	})
	return nil
}

// SessionIDは役割別session IDを返し、初回は新規採番する。
// 2つ目の戻り値は当該sessionがreadyか否か。
func (s *StateStore) SessionID(role SessionRole) (string, bool, error) {
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

// MarkReadyは役割別sessionをready状態へ遷移させる。
func (s *StateStore) MarkReady(role SessionRole) error {
	return s.Touch(string(role) + ".ready")
}

// RemoveUnreadySessionはreadyでないsession IDを削除する。
func (s *StateStore) RemoveUnreadySession(role SessionRole) error {
	if s.Exists(string(role) + ".ready") {
		return nil
	}
	return s.Remove(string(role) + ".id")
}
