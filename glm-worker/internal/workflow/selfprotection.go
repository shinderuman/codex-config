package workflow

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// emptyTreeObjectはgitの空tree object hash。task開始時にcommitが無いrepoやbaseline-head欠落時の
// diff baselineに使い、tracked file全件を変更対象へ含めて保守的にHIGHへ倒す。
const emptyTreeObject = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

type selfProtectionDecision struct {
	High    bool
	Source  string
	HitPath string
}

// IsCriticalPathは自己保護のHIGH対象pathかを判定する。全fileがQA-criticalなpackage(workflow/packet/runner/app/config)
// はproduction .goをpackage-level対象、観測fileと混在するstateは明示file、managed品質規則は対象とする。
// policy file自身はworkflow packageに含まれ本policy変更が自動HIGHとなり、将来追加fileのfail-openを防ぐ。
func IsCriticalPath(path string) (bool, string) {
	if path == "" {
		return false, ""
	}
	switch {
	case isProductionGoUnder(path, "glm-worker/internal/workflow/"):
		return true, "workflow-package"
	case isProductionGoUnder(path, "glm-worker/internal/packet/"):
		return true, "packet-package"
	case isProductionGoUnder(path, "glm-worker/internal/runner/"):
		return true, "runner-package"
	case isProductionGoUnder(path, "glm-worker/internal/app/"):
		return true, "app-package"
	case isProductionGoUnder(path, "glm-worker/internal/config/"):
		return true, "config-package"
	case criticalStateFiles[path]:
		return true, "state-critical"
	case strings.HasPrefix(path, "codex/glm-worker/prompts/"):
		return true, "managed-prompts"
	case strings.HasPrefix(path, "codex/instructions/"):
		return true, "managed-instructions"
	case strings.HasPrefix(path, "codex/rules/"):
		return true, "managed-rules"
	case path == "codex/AGENTS.md":
		return true, "managed-agents"
	}
	return false, ""
}

func isProductionGoUnder(path, dir string) bool {
	if !strings.HasPrefix(path, dir) {
		return false
	}
	if !strings.HasSuffix(path, ".go") {
		return false
	}
	return !strings.HasSuffix(path, "_test.go")
}

// stateはQA-critical fileと観測file(stats.go/telemetry.go)が混在するため、critical側を明示する。
var criticalStateFiles = map[string]bool{
	"glm-worker/internal/state/store.go":      true,
	"glm-worker/internal/state/resume.go":     true,
	"glm-worker/internal/state/baseline.go":   true,
	"glm-worker/internal/state/snapshot.go":   true,
	"glm-worker/internal/state/artifact.go":   true,
	"glm-worker/internal/state/store_util.go": true,
}

func classifySelfProtection(paths []string) selfProtectionDecision {
	categories := make(map[string]struct{})
	var firstHit string
	for _, p := range paths {
		if ok, cat := IsCriticalPath(p); ok {
			categories[cat] = struct{}{}
			if firstHit == "" {
				firstHit = p
			}
		}
	}
	if len(categories) == 0 {
		return selfProtectionDecision{High: false}
	}
	cats := make([]string, 0, len(categories))
	for c := range categories {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	return selfProtectionDecision{High: true, Source: strings.Join(cats, ","), HitPath: firstHit}
}

// collectChangedPathsは現在状態からbaseline-head(欠落時は空tree)からの変更path集合を返す。
// baseline-statusから単純除外せずcommit移動・staged・unstaged・untracked全てを含め、既存critical変更のfail-openを防ぐ。
func collectChangedPaths(repoRoot, baselineHead string) ([]string, error) {
	base := baselineHead
	if strings.TrimSpace(base) == "" {
		base = emptyTreeObject
	}
	tracked, err := exec.Command("git", "-C", repoRoot, "diff", "--no-renames", "--name-only", "-z", base).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}
	untracked, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files --others: %w", err)
	}
	paths := make([]string, 0, 16)
	paths = append(paths, splitNul(tracked)...)
	paths = append(paths, splitNul(untracked)...)
	return paths, nil
}

func splitNul(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			result = append(result, string(p))
		}
	}
	return result
}
