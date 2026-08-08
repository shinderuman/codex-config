package main

import (
	"fmt"
	"os/exec"
)

func captureGitBaseline(config appConfig, state *stateStore) error {
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
		command.Dir = config.RepoRoot
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

func (s *stateStore) BaselineDescription() string {
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
