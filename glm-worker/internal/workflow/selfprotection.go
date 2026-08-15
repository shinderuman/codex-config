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

// pathClassはfile単位の意味分類。critical・非対象の両方を保持し、
// repo全tracked fileが分類なしで残ることをtestが検出できるようにする。
type pathClass struct {
	critical bool
	category string
}

// classifiedFilesはdirectory規則で捕捉できないfile単位の意味分類。
// installer適用経路・merge engine・管理settings内容・依存manifest・両AGENTS.mdはcritical、
// 観測専用state file・docs・repo metadataは非対象とする。
var classifiedFiles = map[string]pathClass{
	"install.sh":                             {true, "installer"},
	".githooks/post-merge":                   {true, "installer"},
	"claude/settings-managed.json":           {true, "managed-claude-settings"},
	"codex/config-managed.toml":              {true, "managed-codex-config"},
	"glm-worker/go.mod":                      {true, "dependency-manifest"},
	"tools/merge-json/go.mod":                {true, "dependency-manifest"},
	"codex/AGENTS.md":                        {true, "managed-agents"},
	"AGENTS.md":                              {true, "repo-agents"},
	"glm-worker/internal/state/stats.go":     {false, "observation"},
	"glm-worker/internal/state/telemetry.go": {false, "observation"},
	"README.md":                              {false, "docs"},
	"EVAL.md":                                {false, "docs"},
	"LICENSE":                                {false, "docs"},
	".gitignore":                             {false, "repo-metadata"},
}

// internalPackageCategoriesはglm-worker/internal配下の既知packageの分類名。
// checkpoint・telemetryへ保存するself-protection source文字列の既存意味を変えないため
// 固定nameへmapする。載らないpackageはinternal-productionへ倒しfail-openを防ぐ。
var internalPackageCategories = map[string]string{
	"workflow":   "workflow-package",
	"packet":     "packet-package",
	"runner":     "runner-package",
	"app":        "app-package",
	"config":     "config-package",
	"state":      "state-critical",
	"autoresume": "autoresume-package",
}

// IsCriticalPathはorchestrator自己変更のself-protection判定。戻り値は(HIGH対象か, 意味分類)。
// 対象は委譲・model routing・prompt/instruction・PACKET・session/resume・provider recovery/
// autoresume・権限/隔離・managed settings/installer適用意味を変更できるproduction surface。
// glm-worker/internal配下のproduction .goはpackage既知・未知を問わず既定HIGHとし、将来の
// internal package追加がfail-openにならない。glm-worker/cmd配下のproduction .goもCLI routing・
// app/workflow gate呼出の生きた境界であるentrypointとしてHIGHに保つ。test file・検証harness・
// docs・観測fileだけの変更は非対象のままLOWに保つ。全tracked fileがcritical・非対象いずれかの分類を持つことは
// unit testが強制し、未分類の新規fileは意味判断を求めてtestを失敗させる。
// policy file自身はworkflow packageに含まれ本policy変更が自動HIGHとなり、判定回避を防ぐ。
func IsCriticalPath(path string) (bool, string) {
	if path == "" {
		return false, ""
	}
	if c, ok := classifiedFiles[path]; ok {
		return c.critical, c.category
	}
	switch {
	case isProductionGoUnder(path, "glm-worker/internal/"):
		return true, internalPackageCategory(path)
	case isProductionGoUnder(path, "glm-worker/cmd/"):
		return true, "worker-entrypoint"
	case isProductionGoUnder(path, "tools/merge-json/"):
		return true, "merge-tool"
	case strings.HasPrefix(path, "glm-worker/scenarios/"):
		return true, "scenario-corpus"
	case strings.HasPrefix(path, "codex/glm-worker/prompts/"):
		return true, "managed-prompts"
	case strings.HasPrefix(path, "codex/instructions/"):
		return true, "managed-instructions"
	case strings.HasPrefix(path, "codex/rules/"):
		return true, "managed-rules"
	case strings.HasSuffix(path, "_test.go"):
		return false, "test"
	case strings.HasPrefix(path, "tests/"), strings.HasPrefix(path, "glm-worker/scripts/"):
		return false, "test-harness"
	}
	return false, ""
}

// internalPackageCategoryはinternal直下の第一階層package名から分類名を引く。
// 未知packageはinternal-productionとして既定HIGHへ分類する。
func internalPackageCategory(path string) string {
	rest := strings.TrimPrefix(path, "glm-worker/internal/")
	pkg := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		pkg = rest[:i]
	}
	if cat, ok := internalPackageCategories[pkg]; ok {
		return cat
	}
	return "internal-production"
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
