package packet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateArtifactsはARTIFACTSがtask専用root配下の実在通常ファイルだけを
// セミコロン区切りで参照していることを検証する。
func ValidateArtifacts(value Packet, root string) error {
	references := strings.TrimSpace(value.Fields["ARTIFACTS"])
	if references == "none" {
		return nil
	}
	if references == "" {
		return &constraintError{reason: "packetに必須field ARTIFACTSがありません"}
	}

	root = filepath.Clean(root)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return &constraintError{reason: fmt.Sprintf("artifact rootを確認できません: %v", err)}
	}
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(references, ";") {
		path := strings.TrimSpace(raw)
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSは正規化済み絶対パスを指定してください: %q", path)}
		}
		if !pathWithinRoot(root, path) {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSは現在taskのartifact dir配下だけを指定してください: %s", path)}
		}
		if _, exists := seen[path]; exists {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSのパスが重複しています: %s", path)}
		}
		seen[path] = struct{}{}

		info, err := os.Lstat(path)
		if err != nil {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSのファイルを確認できません: %s: %v", path, err)}
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSは実在する通常ファイルだけを指定してください: %s", path)}
		}
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil || !pathWithinRoot(resolvedRoot, resolvedPath) {
			return &constraintError{reason: fmt.Sprintf("ARTIFACTSの解決先がartifact dir外です: %s", path)}
		}
	}
	return nil
}

func pathWithinRoot(root string, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
