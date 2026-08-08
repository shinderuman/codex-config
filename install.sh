#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
codex_dir="${CODEX_CONFIG_DIR:-$HOME/.codex}"
bin_dir="${GLM_WORKER_BIN_DIR:-$HOME/.local/bin}"
claude_settings="${CLAUDE_SETTINGS_FILE:-$HOME/.claude/settings.json}"

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

    # 前回codex-configが配置したファイルのうち、
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

    tmp=$(mktemp "${TMPDIR:-/tmp}/codex-config.XXXXXX")
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
        go test ./...
        go build -buildvcs=false -trimpath -o "$build_dir/glm-worker" .
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

merge_claude_settings() {
    mkdir -p "$(dirname "$claude_settings")"

    result=$(
        cd "$repo_root/tools/merge-json"
        go run . \
            -target "$claude_settings" \
            -fragment "$repo_root/claude/settings-managed.json"
    )

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
require install

install_codex_files
merge_codex_config
build_glm_worker
merge_claude_settings
install_pull_hook

mkdir -p "$HOME/.glm-worker/sessions"

printf '%s\n' 'install complete'
