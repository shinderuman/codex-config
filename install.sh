#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
codex_dir="${CODEX_CONFIG_DIR:-$HOME/.codex}"
bin_dir="${GLM_WORKER_BIN_DIR:-$HOME/.local/bin}"
claude_settings="${CLAUDE_SETTINGS_FILE:-$HOME/.claude/settings.json}"
glm_worker_home="${GLM_WORKER_HOME:-$HOME/.glm-worker}"

# final HEADのplan現在状態節へ許さない過渡表現。完了済みcommitのamend/install操作を
# 未実施のpending操作として記述するstale-by-oneの機械判定対象。amend直前・install前・
# amendの前等のpending形式に限定し、先頭のidentifier境界でuninstall前等の別語への誤一致
# を避ける。「amend後」は現在taskの正当な記述(amend後のpostconditionを実装する等)にも
# 使うため対象外とする。
plan_transitional_pattern='(^|[^[:alnum:]_])(amend|install)(する)?(の|直)?前'

require() {
    if ! command -v "$1" >/dev/null 2>&1; then
        printf 'required command not found: %s\n' "$1" >&2
        exit 1
    fi
}

copy_file() {
    src=$1
    dst=$2

    mkdir -p "$(dirname "$dst")"

    if [ -f "$dst" ] && cmp -s "$src" "$dst"; then
        return
    fi

    cp -p "$src" "$dst"
    printf 'updated: %s\n' "$dst"
}

copy_tree() {
    src=$1
    dst=$2

    mkdir -p "$dst"

    # 配布元に存在するファイルだけを配置する。
    # default.rules等、配置先にしかないファイルは削除しない。
    rsync -a "$src/" "$dst/"
}

install_codex_files() {
    manifest_file="$codex_dir/.codex-config-managed-files"
    current_manifest=$(mktemp "${TMPDIR:-/tmp}/codex-managed-current.XXXXXX")
    previous_manifest=$(mktemp "${TMPDIR:-/tmp}/codex-managed-previous.XXXXXX")

    trap 'rm -f "$current_manifest" "$previous_manifest"' EXIT HUP INT TERM

    {
        printf '%s\n' 'AGENTS.md'

        (
            cd "$repo_root/codex"
            find instructions -type f -print
            printf '%s\n' 'rules/glm-worker.rules'
            find glm-worker/prompts -type f -print
        )
    } | LC_ALL=C sort -u >"$current_manifest"

    if [ -f "$manifest_file" ]; then
        cp "$manifest_file" "$previous_manifest"
    else
        : >"$previous_manifest"
    fi

    # 前回codex-worker-orchestratorが配置したファイルのうち、
    # 今回の配布元から消えたものだけを削除する。
    # manifestにないローカルファイルには触れない。
    while IFS= read -r relative_path; do
        [ -n "$relative_path" ] || continue

        if ! grep -Fqx "$relative_path" "$current_manifest"; then
            target="$codex_dir/$relative_path"

            if [ -f "$target" ] || [ -L "$target" ]; then
                rm -f "$target"
                printf 'removed: %s\n' "$target"
            fi
        fi
    done <"$previous_manifest"

    copy_file \
        "$repo_root/codex/AGENTS.md" \
        "$codex_dir/AGENTS.md"

    copy_tree \
        "$repo_root/codex/instructions" \
        "$codex_dir/instructions"

    copy_file \
        "$repo_root/codex/rules/glm-worker.rules" \
        "$codex_dir/rules/glm-worker.rules"

    copy_tree \
        "$repo_root/codex/glm-worker/prompts" \
        "$codex_dir/glm-worker/prompts"

    mkdir -p "$codex_dir"
    cp "$current_manifest" "$manifest_file"

    rm -f "$current_manifest" "$previous_manifest"
    trap - EXIT HUP INT TERM
}

merge_codex_config() {
    config_file="$codex_dir/config.toml"
    managed_file="$repo_root/codex/config-managed.toml"

    managed_value=$(
        awk -F= '
            /^[[:space:]]*background_terminal_max_timeout[[:space:]]*=/ {
                value = $2
                gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
                print value
                exit
            }
        ' "$managed_file"
    )

    if [ -z "$managed_value" ]; then
        printf '%s\n' 'background_terminal_max_timeout is missing from config-managed.toml' >&2
        exit 1
    fi

    tmp=$(mktemp "${TMPDIR:-/tmp}/codex-worker-orchestrator.XXXXXX")
    trap 'rm -f "$tmp"' EXIT HUP INT TERM

    if [ -f "$config_file" ]; then
        awk -v value="$managed_value" '
            BEGIN {
                top = 1
                written = 0
            }

            top && /^[[:space:]]*background_terminal_max_timeout[[:space:]]*=/ {
                if (!written) {
                    print "background_terminal_max_timeout = " value
                    written = 1
                }
                next
            }

            top && /^[[:space:]]*\[/ {
                if (!written) {
                    print "background_terminal_max_timeout = " value
                    print ""
                    written = 1
                }
                top = 0
            }

            {
                print
            }

            END {
                if (!written) {
                    if (NR > 0) {
                        print ""
                    }
                    print "background_terminal_max_timeout = " value
                }
            }
        ' "$config_file" >"$tmp"
    else
        printf 'background_terminal_max_timeout = %s\n' "$managed_value" >"$tmp"
    fi

    if [ -f "$config_file" ] && cmp -s "$tmp" "$config_file"; then
        rm -f "$tmp"
        trap - EXIT HUP INT TERM
        return
    fi

    mkdir -p "$(dirname "$config_file")"
    mv "$tmp" "$config_file"
    trap - EXIT HUP INT TERM
    printf 'updated: %s\n' "$config_file"
}

build_glm_worker() {
    build_dir=$(mktemp -d "${TMPDIR:-/tmp}/glm-worker-build.XXXXXX")
    trap 'rm -rf "$build_dir"' EXIT HUP INT TERM

    (
        cd "$repo_root/glm-worker"
        go build -buildvcs=false -trimpath -o "$build_dir/glm-worker" ./cmd/glm-worker
    )

    mkdir -p "$bin_dir"

    if [ -f "$bin_dir/glm-worker" ] && cmp -s "$build_dir/glm-worker" "$bin_dir/glm-worker"; then
        printf '%s\n' 'glm-worker: unchanged'
    else
        install -m 0755 "$build_dir/glm-worker" "$bin_dir/glm-worker"
        printf 'installed: %s\n' "$bin_dir/glm-worker"
    fi

    rm -rf "$build_dir"
    trap - EXIT HUP INT TERM
}

# verify_claude_cliはglm-workerのstructured output契約(--json-schema flag)をCLI側が
# 支援しているかをAI呼び出しなしで確認する。PATH上に見つかるbinaryだけが検証対象で、
# 見つからない場合はruntime構成(GLM_WORKER_CLAUDE_BIN)へ委ねてinstallは続行する。
# 見つかったbinaryが--json-schemaを案内しない場合は全model呼出がfail closedになるため
# install時点で失敗させる。
verify_claude_cli() {
    claude_bin="${GLM_WORKER_CLAUDE_BIN:-claude}"
    if ! command -v "$claude_bin" >/dev/null 2>&1; then
        printf 'claude cli: %s not on PATH; skipping contract check (glm-worker requires it at runtime)\n' "$claude_bin"
        return 0
    fi
    if ! "$claude_bin" --help 2>/dev/null | grep -q -- '--json-schema'; then
        printf 'claude cli: %s does not support --json-schema; upgrade Claude Code before install\n' "$claude_bin" >&2
        return 1
    fi
    printf 'claude cli: %s supports --json-schema\n' "$claude_bin"
}

# plan_sectionはstdinのplan本文から見出しpatternに一致する`## `節の本文を取り出す。
# 節の終わりは次の`## `見出し行で、項目はRULESが定めるunordered marker `-`だけを対象にする。
plan_section() {
    awk -v pattern="$1" '
        /^## / {
            if (in_section) {
                exit
            }
            in_section = $0 ~ pattern
            next
        }
        in_section {
            print
        }
    '
}

# plan_bullet_pathsはstdinのplan本文から全unordered bulletを1行1項目で取り出す。
# bullet検出とpath抽出はruntime activeSectionEntries/activeEntryPathと同じ規則で、行両端の
# 空白を除去してから`- `markerを判定し、逆引用符は項目全体を1組で囲む場合だけpath区切りとして
# 扱う。逆引用符なしの直書きは行全体をpath候補とする。閉じ欠損・前後の余分なtext・複数組の
# malformed bulletと認識できないpath候補を黙って捨てるとfail openになるため、malformedは
# 先頭marker `!` 、path候補は `+` を付けて呼出側へ渡す。
plan_bullet_paths() {
    awk '
        {
            line = $0
            sub(/^[[:space:]]+/, "", line)
            sub(/[[:space:]]+$/, "", line)
            if (substr(line, 1, 2) != "- ") {
                next
            }
            line = substr(line, 3)
            sub(/^[[:space:]]+/, "", line)
            ticks = gsub(/`/, "`", line)
            if (ticks == 0) {
                printf "+%s\n", line
            } else if (ticks == 2 && substr(line, 1, 1) == "`" && substr(line, length(line), 1) == "`") {
                printf "+%s\n", substr(line, 2, length(line) - 2)
            } else {
                printf "!%s\n", line
            }
        }
    '
}

# validate_plan_task_pathはplanが記述するtask pathがruntime配置契約(workflowの
# validateActiveTaskPath)と同じ規則へ収まることを確認する。IMPLEMENTATION_TASKS/
# prefix・.md suffix・空segment・`.`・`..`・二重slash・backslashを拒否する。番号prefixは
# 契約外のため要求せず、subdirectoryは許容する。受理集合のruntimeとの一致は
# TestPlanFinalHeadTaskPathValidatorMatchesRuntimeが固定する。
validate_plan_task_path() {
    case $1 in
        IMPLEMENTATION_TASKS/*.md) ;;
        *) return 1 ;;
    esac
    case $1 in
        *\\* | *//* | */./* | */../*) return 1 ;;
    esac
    return 0
}

# plan_final_head_failはfinal HEAD postcondition gateの失敗出力と復旧経路を固定する。
# 追加commitの連鎖やworking treeだけの修正で通過扱いにさせないため、同一commitへの
# amendだけを復旧経路として案内する。
plan_final_head_fail() {
    printf 'plan final head: %s\n' "$1" >&2
    printf '%s\n' 'plan final head: IMPLEMENTATION_RULES.mdのtask完了契約に従いPlan・IMPLEMENTATION_TASKS・Historyを同期し、同一commitへamendしてからinstallすること。同期済みfinal HEADになるまでinstall・次task・handoffへ進まない' >&2
    return 1
}

# require_head_task_fileはplanが参照するtask fileがHEAD treeへregular fileとして存在する
# ことを確認する。symlinkとdirectoryは要求正本として受理しない。
require_head_task_file() {
    referenced_path=$1
    referenced_mode=$(git -C "$repo_root" ls-tree HEAD -- "$referenced_path" | awk '{print $1; exit}')
    case $referenced_mode in
        100644 | 100755) ;;
        *)
            plan_final_head_fail "HEADのplanが参照するtask file $referenced_path がHEAD treeへregular fileとして存在しません"
            return 1
            ;;
    esac
}

# verify_plan_final_headはtracked canonical planのfinal HEAD postconditionをmanaged配置の
# 前段階として機械強制する。親Codexのtask完了契約は完了taskのmetadata同期(History移行・
# task file削除・NEXT昇格)を同一commitへamendしてfinal HEADへ収めるが、手順実施を同期
# 保証と同一視してamendを飛ばすとfinal HEADのplanだけが完了済みcommitを「amend直前」
# 「install前」等の未実施操作として記述したstale-by-oneになる。gateはHEAD収録planの
# ACTIVE/NEXT/task file存在/Git境界を判定し、現在状態節の過渡表現を拒否する。判定は
# HEADだけで行い、dirty working treeのplanやplanを置かない他repositoryの通常installは
# 妨げない。
verify_plan_final_head() {
    if ! git -C "$repo_root" rev-parse --git-dir >/dev/null 2>&1; then
        printf '%s\n' 'plan final head: skipped (not a git repository)'
        return 0
    fi
    if ! git -C "$repo_root" rev-parse --verify HEAD >/dev/null 2>&1; then
        printf '%s\n' 'plan final head: skipped (no commits)'
        return 0
    fi
    if [ -z "$(git -C "$repo_root" ls-files -- IMPLEMENTATION_PLAN.local.md)" ]; then
        printf '%s\n' 'plan final head: skipped (IMPLEMENTATION_PLAN.local.md is untracked)'
        return 0
    fi
    if ! plan_head=$(git -C "$repo_root" show HEAD:IMPLEMENTATION_PLAN.local.md 2>/dev/null); then
        printf '%s\n' 'plan final head: skipped (IMPLEMENTATION_PLAN.local.md is not in HEAD yet)'
        return 0
    fi

    active_entries=$(printf '%s\n' "$plan_head" | plan_section '^## ACTIVE[[:space:]]*$' | grep -c '^[[:space:]]*- ' || true)
    if [ "$active_entries" -eq 0 ]; then
        plan_final_head_fail "HEADのIMPLEMENTATION_PLAN.local.mdのACTIVE欄にtask fileがありません"
        return 1
    fi
    if [ "$active_entries" -gt 1 ]; then
        plan_final_head_fail "HEADのIMPLEMENTATION_PLAN.local.mdのACTIVE欄が一意ではありません(${active_entries}件)"
        return 1
    fi

    active_path=$(printf '%s\n' "$plan_head" | plan_section '^## ACTIVE[[:space:]]*$' | plan_bullet_paths)
    case $active_path in
        '!'*)
            plan_final_head_fail "HEADのplanのACTIVE欄にbullet構文(逆引用符1組で囲まれた単一task path、または逆引用符なしの直書き)へ違反している項目があります: ${active_path#!}"
            return 1
            ;;
    esac
    active_path=${active_path#+}
    if ! validate_plan_task_path "$active_path"; then
        plan_final_head_fail "HEADのplanのACTIVE欄がtask path契約(IMPLEMENTATION_TASKS/配下の.md・配置契約準拠)へ違反しています: ${active_path:-(解決できません)}"
        return 1
    fi
    require_head_task_file "$active_path" || return 1

    # NEXT/BLOCKEDは空sectionを許容するが、bulletが存在するなら全てvalid task pathへ
    # 解決されることを検証する。認識できないbulletを無視するとinvalid entryがgateを
    # 通過するため、path検証はbullet単位でfail closedする。
    scheduled_tasks=$(
        printf '%s\n' "$plan_head" | plan_section '^## NEXT' | plan_bullet_paths
        printf '%s\n' "$plan_head" | plan_section '^## BLOCKED' | plan_bullet_paths
    )
    scheduled_paths=''
    while IFS= read -r scheduled_entry; do
        [ -n "$scheduled_entry" ] || continue
        case $scheduled_entry in
            '!'*)
                plan_final_head_fail "HEADのplanのNEXT/BLOCKED欄にbullet構文(逆引用符1組で囲まれた単一task path、または逆引用符なしの直書き)へ違反している項目があります: ${scheduled_entry#!}"
                return 1
                ;;
        esac
        scheduled_path=${scheduled_entry#+}
        if ! validate_plan_task_path "$scheduled_path"; then
            plan_final_head_fail "HEADのplanのNEXT/BLOCKED欄にtask pathへ解決できない項目があります: ${scheduled_path:-(空)}"
            return 1
        fi
        require_head_task_file "$scheduled_path" || return 1
        scheduled_paths="$scheduled_paths$scheduled_path
"
    done <<EOF
$scheduled_tasks
EOF
    if printf '%s\n' "$scheduled_paths" | grep -Fxq "$active_path"; then
        plan_final_head_fail "HEADのplanのACTIVE task file $active_path がNEXT/BLOCKEDへ重複して記載されています"
        return 1
    fi

    boundary_branch=$(
        printf '%s\n' "$plan_head" | plan_section '^## 現在のGit境界' | awk '
            $0 ~ /^[[:space:]]*-[[:space:]]*branch:/ {
                line = $0
                sub(/^[^:]*:[[:space:]]*/, "", line)
                tick = index(line, "`")
                if (tick > 0) {
                    line = substr(line, tick + 1)
                    end = index(line, "`")
                    if (end > 0) {
                        line = substr(line, 1, end - 1)
                    }
                }
                print line
                exit
            }
        '
    )
    if [ -n "$boundary_branch" ]; then
        current_branch=$(git -C "$repo_root" branch --show-current)
        if [ "$boundary_branch" != "$current_branch" ]; then
            plan_final_head_fail "HEADのplanのGit境界branch ${boundary_branch}が現在のbranch(${current_branch:-detached HEAD})と矛盾しています"
            return 1
        fi
    fi

    # 完了済みcommitの操作をamend直前・install前等の未実施pendingとして記述する過渡表現は
    # final HEADではもう存在できない。対象は現在状態を記述する3節だけとし、次taskの作業中planは
    # commit前のworking treeだけに置ける。境界のnegated classはBSD grepのUTF-8 localeで
    # multibyte前置文字に一致しないため、判定はbyte志向のLC_ALL=Cで行いplatform間で同じ
    # 挙動に固定する。
    transitional=$(
        {
            printf '%s\n' "$plan_head" | plan_section '^## 現在のGit境界' | LC_ALL=C grep -E "$plan_transitional_pattern" || true
            printf '%s\n' "$plan_head" | plan_section '^## 現在の停止理由' | LC_ALL=C grep -E "$plan_transitional_pattern" || true
            printf '%s\n' "$plan_head" | plan_section '^## 次の親Codex操作' | LC_ALL=C grep -E "$plan_transitional_pattern" || true
        }
    )
    if [ -n "$transitional" ]; then
        plan_final_head_fail "HEADのplanの現在状態記述が完了済みcommitの操作を未実施としています: $transitional"
        return 1
    fi

    printf '%s\n' 'plan final head: verified'
}

preflight() {
    build_dir=$(mktemp -d "${TMPDIR:-/tmp}/codex-worker-orchestrator-preflight.XXXXXX")

    # if ! (...)内ではerrexitが無視され、subshell全体の成否は最後のcommandだけで決まる。
    # 失敗を後続の成功で上書きさせないため、各commandは失敗時に明示的に非0でsubshellを抜ける。
    if ! (
        cd "$repo_root/glm-worker" || exit
        go test ./... || exit
        go build -buildvcs=false -trimpath -o "$build_dir/glm-worker" ./cmd/glm-worker || exit

        cd "$repo_root/tools/merge-json" || exit
        go test ./... || exit
        go build -buildvcs=false -trimpath -o "$build_dir/merge-json" . || exit
    ); then
        rm -rf "$build_dir"
        return 1
    fi

    rm -rf "$build_dir"
}

merge_claude_settings() {
    mkdir -p "$(dirname "$claude_settings")"

    override_path="${CODEX_CONFIG_CLAUDE_SETTINGS_OVERRIDE:-${XDG_CONFIG_HOME:-$HOME/.config}/codex-config/claude-settings.local.json}"

    if [ -f "$override_path" ]; then
        result=$(
            cd "$repo_root/tools/merge-json"
            go run . \
                -target "$claude_settings" \
                -fragment "$repo_root/claude/settings-managed.json" \
                -env-override "$override_path"
        )
    else
        result=$(
            cd "$repo_root/tools/merge-json"
            go run . \
                -target "$claude_settings" \
                -fragment "$repo_root/claude/settings-managed.json"
        )
    fi

    printf 'claude settings: %s\n' "$result"
}

install_pull_hook() {
    if ! git -C "$repo_root" rev-parse --git-dir >/dev/null 2>&1; then
        printf '%s\n' 'git hook: skipped'
        return
    fi

    chmod +x "$repo_root/.githooks/post-merge"

    current=$(git -C "$repo_root" config --local --get core.hooksPath || true)
    if [ "$current" = '.githooks' ]; then
        printf '%s\n' 'git hook: unchanged'
        return
    fi

    git -C "$repo_root" config --local core.hooksPath .githooks
    printf '%s\n' 'git hook: enabled'
}

require git
require go
require rsync
require cmp
require awk
require grep
require install

verify_plan_final_head || exit $?
printf '%s\n' 'preflight: validating source before applying managed files'
preflight || exit $?
verify_claude_cli || exit $?
build_glm_worker
install_codex_files
merge_codex_config
merge_claude_settings
install_pull_hook

mkdir -p "$glm_worker_home/sessions"

printf '%s\n' 'install complete'
printf '%s\n' 'Codexルールの再読込を保証するには、新しいCodexタスクを開始してください。'
