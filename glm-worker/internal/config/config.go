// Package configは環境変数とgitからアプリ全体の設定を構築する。
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// AppConfigはglm-worker全体で共有される設定。
type AppConfig struct {
	RepoRoot              string
	RepoHash              string
	RepoShort             string
	StateBase             string
	PromptDir             string
	ClaudeBin             string
	WorkerModel           string
	ReviewerModel         string
	HighRiskReviewerModel string
	RoutineEffort         string
	EscalatedEffort       string
	MaxAutoFixRounds      int
	TelemetryContent      bool
}

// Loadは環境変数とgitからAppConfigを構築する。
func Load() (AppConfig, error) {
	repoRoot, err := resolveRepoRoot()
	if err != nil {
		return AppConfig{}, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return AppConfig{}, fmt.Errorf("ホームディレクトリを取得できません: %w", err)
	}

	repoHash := sha256.Sum256([]byte(repoRoot))
	repoHashString := hex.EncodeToString(repoHash[:])

	stateHome := envOrDefault("GLM_WORKER_HOME", filepath.Join(home, ".glm-worker"))
	promptDir := envOrDefault("GLM_WORKER_PROMPT_DIR", filepath.Join(home, ".codex", "glm-worker", "prompts"))
	rounds, err := intEnv("GLM_WORKER_MAX_AUTO_FIX_ROUNDS", 2)
	if err != nil {
		return AppConfig{}, err
	}
	telemetryContent, err := boolEnv("GLM_WORKER_TELEMETRY_CONTENT", true)
	if err != nil {
		return AppConfig{}, err
	}

	return AppConfig{
		RepoRoot:              repoRoot,
		RepoHash:              repoHashString,
		RepoShort:             repoHashString[:12],
		StateBase:             filepath.Join(stateHome, "sessions"),
		PromptDir:             promptDir,
		ClaudeBin:             envOrDefault("GLM_WORKER_CLAUDE_BIN", "claude"),
		WorkerModel:           envOrDefault("GLM_WORKER_WORKER_MODEL", "opus"),
		ReviewerModel:         envOrDefault("GLM_WORKER_REVIEWER_MODEL", "haiku"),
		HighRiskReviewerModel: envOrDefault("GLM_WORKER_HIGH_RISK_REVIEWER_MODEL", "sonnet"),
		RoutineEffort:         envOrDefault("GLM_WORKER_EFFORT", "high"),
		EscalatedEffort:       envOrDefault("GLM_WORKER_ESCALATED_EFFORT", "max"),
		MaxAutoFixRounds:      rounds,
		TelemetryContent:      telemetryContent,
	}, nil
}

// resolveRepoRootはgitのtop-levelを優先し、失敗時はcwdを解決する。
func resolveRepoRoot() (string, error) {
	command := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err == nil {
		root := strings.TrimSpace(string(output))
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

func intEnv(name string, defaultValue int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultValue, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%sは0以上の整数で指定してください", name)
	}
	return value, nil
}

func boolEnv(name string, defaultValue bool) (bool, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%sは真偽値で指定してください", name)
	}
	return value, nil
}
