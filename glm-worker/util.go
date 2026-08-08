package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func stringTrimSpace(value []byte) string {
	return strings.TrimSpace(string(value))
}

func newUUID() (string, error) {
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

func envWithDefaults(base []string, defaults map[string]string) []string {
	result := append([]string(nil), base...)
	present := make(map[string]bool)

	for _, item := range base {
		if index := strings.IndexByte(item, '='); index >= 0 {
			present[item[:index]] = true
		}
	}

	for key, value := range defaults {
		if !present[key] {
			result = append(result, key+"="+value)
		}
	}

	return result
}

func equalBytes(a []byte, b []byte) bool {
	return bytes.Equal(a, b)
}
