package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

type appConfig struct {
	RepoRoot         string
	RepoHash         string
	RepoShort        string
	StateBase        string
	PromptDir        string
	ClaudeBin        string
	Effort           string
	MaxAutoFixRounds int
}

func loadConfig() (appConfig, error) {
	repoRoot, err := resolveRepoRoot()
	if err != nil {
		return appConfig{}, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return appConfig{}, fmt.Errorf("ホームディレクトリを取得できません: %w", err)
	}

	repoHash := sha256.Sum256([]byte(repoRoot))
	repoHashString := hex.EncodeToString(repoHash[:])

	stateHome := envOrDefault("GLM_WORKER_HOME", filepath.Join(home, ".glm-worker"))
	promptDir := envOrDefault("GLM_WORKER_PROMPT_DIR", filepath.Join(home, ".codex", "glm-worker", "prompts"))
	rounds, err := intEnv("GLM_WORKER_MAX_AUTO_FIX_ROUNDS", "GLM_WORKER_MAX_REVIEW_ROUNDS", 2)
	if err != nil {
		return appConfig{}, err
	}

	return appConfig{
		RepoRoot:         repoRoot,
		RepoHash:         repoHashString,
		RepoShort:        repoHashString[:12],
		StateBase:        filepath.Join(stateHome, "sessions"),
		PromptDir:        promptDir,
		ClaudeBin:        envOrDefault("GLM_WORKER_CLAUDE_BIN", "claude"),
		Effort:           envOrDefault("GLM_WORKER_EFFORT", "max"),
		MaxAutoFixRounds: rounds,
	}, nil
}

func resolveRepoRoot() (string, error) {
	command := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err == nil {
		root := stringTrimSpace(output)
		return filepath.EvalSymlinks(root)
	}

	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return "", fmt.Errorf("作業ディレクトリを取得できません: %w", cwdErr)
	}

	root, evalErr := filepath.EvalSymlinks(cwd)
	if evalErr != nil {
		return "", fmt.Errorf("作業ディレクトリを解決できません: %w", evalErr)
	}

	return root, nil
}

func envOrDefault(name string, defaultValue string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return defaultValue
}

func intEnv(primary string, legacy string, defaultValue int) (int, error) {
	raw := os.Getenv(primary)
	if raw == "" && legacy != "" {
		raw = os.Getenv(legacy)
	}
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%sは0以上の整数で指定してください", primary)
	}
	return value, nil
}
