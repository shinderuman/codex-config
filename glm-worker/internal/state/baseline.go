package state

import (
	"fmt"
	"os/exec"

	"github.com/shinderuman/codex-config/glm-worker/internal/config"
)

// CaptureGitBaselineはタスク開始前のgit状態をstateへ保存する。
// gitの取得失敗時はbaselineを取り下げ(削除)しエラーとはしない。
func CaptureGitBaseline(cfg config.AppConfig, state *StateStore) error {
	commands := []struct {
		name string
		args []string
	}{
		{name: "baseline-status", args: []string{"status", "--porcelain=v1", "--untracked-files=all"}},
		{name: "baseline-worktree.patch", args: []string{"diff", "--binary", "--no-ext-diff"}},
		{name: "baseline-index.patch", args: []string{"diff", "--cached", "--binary", "--no-ext-diff"}},
	}

	for _, item := range commands {
		command := exec.Command("git", item.args...)
		command.Dir = cfg.RepoRoot
		output, err := command.Output()
		if err != nil {
			if err := state.Remove("baseline-status", "baseline-worktree.patch", "baseline-index.patch"); err != nil {
				return err
			}
			return nil
		}
		if err := state.Write(item.name, string(output)); err != nil {
			return err
		}
	}
	return nil
}

// BaselineDescriptionはreviewerへ示すbaseline情報を返す。
func (s *StateStore) BaselineDescription() string {
	if !s.Exists("baseline-status") {
		return "none"
	}

	return fmt.Sprintf(
		"status=%s\nworktree_diff=%s\nstaged_diff=%s",
		s.Path("baseline-status"),
		s.Path("baseline-worktree.patch"),
		s.Path("baseline-index.patch"),
	)
}
