package state

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewUUIDはUUID version 4形式の文字列を生成する。
func NewUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("session UUIDを生成できません: %w", err)
	}

	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}

// ValidGeneratedUUIDはNewUUIDが生成する形式(長さ36・hyphen位置8/13/18/23・小写hex・
// version 4・variant 10x)に完全一致するかだけを判定する。CLI引数やstate fileなど外部
// 由来IDからstate dir内pathを構築する前の境界検証に使うため、filesystemへは触れない。
// filepath.Base比較やJoin後のprefix確認と違い、この形式一致は"../"・絶対path・上位version・
// 不正variantをすべて生成形式の外へ落とす。
func ValidGeneratedUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for index := 0; index < len(id); index++ {
		switch index {
		case 8, 13, 18, 23:
			if id[index] != '-' {
				return false
			}
		default:
			if !isLowerHexDigit(id[index]) {
				return false
			}
		}
	}
	return id[14] == '4' && strings.IndexByte("89ab", id[19]) >= 0
}

func isLowerHexDigit(char byte) bool {
	return (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()

	defer func() {
		file.Close()
		os.Remove(tempPath)
	}()

	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	return os.Rename(tempPath, path)
}
