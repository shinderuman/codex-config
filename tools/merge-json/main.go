package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

func main() {
	target := flag.String("target", "", "merge target JSON file")
	fragment := flag.String("fragment", "", "managed JSON fragment")
	flag.Parse()

	if *target == "" || *fragment == "" {
		fail("-target and -fragment are required")
	}

	changed, err := mergeFiles(*target, *fragment)
	if err != nil {
		fail(err.Error())
	}

	if changed {
		fmt.Println("updated")
	} else {
		fmt.Println("unchanged")
	}
}

func mergeFiles(targetPath string, fragmentPath string) (bool, error) {
	target, targetMode, err := readObjectOrEmpty(targetPath)
	if err != nil {
		return false, fmt.Errorf("target JSON: %w", err)
	}

	fragment, _, err := readObject(fragmentPath)
	if err != nil {
		return false, fmt.Errorf("fragment JSON: %w", err)
	}

	before := cloneMap(target)
	deepMerge(target, fragment)
	if reflect.DeepEqual(before, target) {
		return false, nil
	}

	data, err := json.MarshalIndent(target, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')

	if err := writeAtomic(targetPath, data, targetMode); err != nil {
		return false, err
	}
	return true, nil
}

func readObjectOrEmpty(path string) (map[string]any, os.FileMode, error) {
	object, mode, err := readObject(path)
	if os.IsNotExist(err) {
		return map[string]any{}, 0o600, nil
	}
	return object, mode, err
}

func readObject(path string) (map[string]any, os.FileMode, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}

	decoder := json.NewDecoder(file)
	decoder.UseNumber()

	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, 0, err
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, 0, fmt.Errorf("multiple JSON values")
		}
		return nil, 0, err
	}

	if object == nil {
		return nil, 0, fmt.Errorf("top-level value must be an object")
	}
	return object, stat.Mode().Perm(), nil
}

func deepMerge(target map[string]any, fragment map[string]any) {
	for key, fragmentValue := range fragment {
		fragmentMap, fragmentIsMap := fragmentValue.(map[string]any)
		targetMap, targetIsMap := target[key].(map[string]any)
		if fragmentIsMap && targetIsMap {
			deepMerge(targetMap, fragmentMap)
			continue
		}
		target[key] = fragmentValue
	}
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		if child, ok := item.(map[string]any); ok {
			result[key] = cloneMap(child)
			continue
		}
		result[key] = item
	}
	return result
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o600
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".merge-json-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	if _, err := bytes.NewReader(data).WriteTo(file); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
