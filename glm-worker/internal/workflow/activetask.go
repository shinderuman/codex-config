package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shinderuman/codex-worker-orchestrator/glm-worker/internal/state"
)

// activeTaskStateKeyはExecuteNewTaskで解決したACTIVE task file pathをtask単位で保持するstate名。
// decision/fix/reviewer/auto-fix各呼出のprompt構築と、worker呼出前の存在確認が同じ値を読む。
// PlanのACTIVE欄は親Codexが停止中に切り替え得るため、実行中taskの要求正本pathはtask開始時点へ
// 固定し、毎回のPlan再解決で別taskの要求定義へすり替わらないようにする。新task開始時は前taskの
// 値を除去し、初回解決成功時だけ設定する。planの無いrepoでは空値を設定済みにして配線なしを表し、
// 解決fail closed後は未設定のまま残すため、親修復後の同一task継続時に再解決して固定し直す。
const activeTaskStateKey = "active-task"

// activeTaskPathPrefixはACTIVE task fileの配置契約。RULESがPlanのACTIVE欄へ許すのは
// `IMPLEMENTATION_TASKS/`配下の`.md` fileだけである(番号付き・semantic filenameの両方を許容し、
// 配下subdirectoryも許容する)。解決対象はsymlinkを辿らないregular fileに限る。
const activeTaskPathPrefix = state.ParentTasksDir + "/"

// activeTaskPathExtはACTIVE task fileの拡張子契約。
const activeTaskPathExt = ".md"

// resolveActiveTaskPathは`IMPLEMENTATION_PLAN.local.md`のACTIVE欄からACTIVE task fileの
// repository相対pathを解決する。planが存在しないrepositoryでは配線なし(wired=false, err=nil)を
// 返し、既存の通常作業を許可する。planが存在する場合はACTIVE欄が一意・bullet構文
// (逆引用符1組の単一task pathか逆引用符なし直書き)・配置契約内(`.md`)・path escapeなし・
// 参照先がsymlinkを辿らないregular fileとしてworking treeへ実在することを全て
// 満たす必要があり、欠けた場合は要求正本を特定できないためerrorを返し呼出元がmodel呼出前に
// fail closedする。
func resolveActiveTaskPath(repoRoot string) (string, bool, error) {
	planPath := filepath.Join(repoRoot, implementationPlanFile)
	content, err := os.ReadFile(planPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", true, fmt.Errorf("read %s: %w", implementationPlanFile, err)
	}
	entries, err := activeSectionEntries(string(content))
	if err != nil {
		return "", true, err
	}
	if len(entries) == 0 {
		return "", true, fmt.Errorf("%sのACTIVE欄にtask fileがありません", implementationPlanFile)
	}
	if len(entries) > 1 {
		return "", true, fmt.Errorf("%sのACTIVE欄が一意ではありません(%d件)", implementationPlanFile, len(entries))
	}
	path := entries[0]
	if err := validateActiveTaskPath(path); err != nil {
		return "", true, err
	}
	info, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(path)))
	if err != nil {
		return "", true, fmt.Errorf("ACTIVE task file %sを確認できません: %w", path, err)
	}
	// symlink・directory・FIFO等は要求正本として受理しない。symlinkはos.Statがrepository外へ
	// 辿るため、Lstatのfile typeで展開前に拒否する。
	if !info.Mode().IsRegular() {
		return "", true, fmt.Errorf("ACTIVE task file %sはregular fileではありません(%s)", path, info.Mode().Type())
	}
	return path, true, nil
}

// activeSectionEntriesはplan本文の`## ACTIVE`節からlist項目を取り出す。節の終わりは次の
// `## `見出し行で、項目はRULESが定めるunordered marker `-`だけを対象にする。
func activeSectionEntries(planContent string) ([]string, error) {
	lines := strings.Split(planContent, "\n")
	inSection := false
	var entries []string
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if inSection {
				break
			}
			inSection = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "ACTIVE"
			continue
		}
		if !inSection {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		path, err := activeEntryPath(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		if err != nil {
			return nil, err
		}
		entries = append(entries, path)
	}
	return entries, nil
}

// activeEntryPathはlist項目からtask path候補を取り出す。標準形式は
// 「- `IMPLEMENTATION_TASKS/....md`」で、逆引用符なしの直書きも受理する。逆引用符は
// 項目全体を1組で囲む場合だけpath区切りとして扱い、閉じ欠損・前後の余分なtext・複数組は
// bullet構文違反としてerrorを返す。installer final HEAD gateのplan_bullet_pathsと同じ
// 規則であり、受理集合の一致はTestPlanFinalHeadBulletExtractionMatchesRuntimeが固定する。
func activeEntryPath(item string) (string, error) {
	switch strings.Count(item, "`") {
	case 0:
		return item, nil
	case 2:
		if strings.HasPrefix(item, "`") && strings.HasSuffix(item, "`") {
			return item[1 : len(item)-1], nil
		}
	}
	return "", fmt.Errorf("ACTIVE欄の項目 %qがbullet構文(逆引用符1組で囲まれた単一task path、または逆引用符なしの直書き)へ違反しています", item)
}

// validateActiveTaskPathはACTIVE task pathが`IMPLEMENTATION_TASKS/`配下の`.md` file契約へ収まり
// repository境界を越えないことを確認する。空segment・`.`・`..`・絶対path・区切り文字の混在・
// `.md`以外の拡張子は全て拒否する。番号prefixは契約外のため要求しない。
func validateActiveTaskPath(path string) error {
	if !strings.HasPrefix(path, activeTaskPathPrefix) {
		return fmt.Errorf("ACTIVE task path %qは%s配下である必要があります", path, state.ParentTasksDir)
	}
	rest := strings.TrimPrefix(path, activeTaskPathPrefix)
	if rest == "" || strings.Contains(path, `\`) || strings.Contains(rest, "//") {
		return fmt.Errorf("ACTIVE task path %qが配置契約に違反しています", path)
	}
	if !strings.HasSuffix(path, activeTaskPathExt) {
		return fmt.Errorf("ACTIVE task path %qが配置契約に違反しています(%s fileに限定)", path, activeTaskPathExt)
	}
	for _, segment := range strings.Split(rest, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("ACTIVE task path %qが配置契約に違反しています", path)
		}
	}
	return nil
}

// readActiveTaskStateは現在taskへ固定済みのACTIVE task file pathを読む。未設定(空)は
// 配線の無いtask・repositoryを表し、呼出元はpromptへ要求源blockを付けない。
func (w *Workflow) readActiveTaskState() string {
	return w.state.ReadOr(activeTaskStateKey, "")
}

// activeTaskStateSetはactive-task stateが書き込まれているかを返す。planの無い正常な
// repositoryでは初回解決成功時に空値を設定済みにするため、未設定はACTIVE解決fail closed
// 後に親CodexがPlan・task fileを修復して同じtaskを再開しようとしている状態だけを意味する。
func (w *Workflow) activeTaskStateSet() bool {
	return w.state.Exists(activeTaskStateKey)
}

// ensureActiveTaskPathは継続呼出(decision・fix・reviewer・auto-fix)開始時に現在taskの
// ACTIVE task file pathを確定させる。設定済みならその値をそのまま使い、planの無いrepoの
// 空値も再解決しない。PlanのACTIVE欄は停止中に親Codexが切り替え得るため設定済み値の
// 再解決は実行中taskの要求正本を別taskへすり替えるだけである。未設定はACTIVE解決fail
// closed後の同一task再開だけを意味するためPlanから再解決して固定し、再解決にも失敗する
// 場合はmodel呼出前に再度fail closedする。
func (w *Workflow) ensureActiveTaskPath(phase string) (string, error) {
	if w.activeTaskStateSet() {
		return w.readActiveTaskState(), nil
	}
	activeTaskPath, wired, err := resolveActiveTaskPath(w.config.RepoRoot)
	if err != nil {
		return "", w.failClosedActiveTaskResolution(phase, err)
	}
	if !wired {
		activeTaskPath = ""
	}
	if err := w.state.Write(activeTaskStateKey, activeTaskPath); err != nil {
		return "", err
	}
	return activeTaskPath, nil
}

// activeTaskFileExistsは固定済みACTIVE task fileが現在もworking treeへregular fileとして存在するかを
// 確認する。解決時と同じくsymlinkは辿らないため、固定後のsymlink・directory差し替えも欠損扱いで
// model呼出前のfail closedへ流れる。
func activeTaskFileExists(repoRoot string, activeTaskPath string) bool {
	if activeTaskPath == "" {
		return true
	}
	info, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(activeTaskPath)))
	return err == nil && info.Mode().IsRegular()
}
