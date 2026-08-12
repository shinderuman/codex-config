package autoresume

import (
	"fmt"
	"strings"
)

// parseAutomationTOMLはautomation.tomlのknown scalarだけを抽出する行指向parser。
// 非公開schemaへ必要以上に結合せず、複雑なescapeやtable構造は扱わない。
func parseAutomationTOML(data []byte) (AutomationTOML, error) {
	values := make(map[string]string)
	bare := make(map[string]bool)

	for i, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		eq := strings.Index(line, "=")
		if eq < 0 {
			return AutomationTOML{}, fmt.Errorf("line %d: key=value separator missing", i+1)
		}

		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return AutomationTOML{}, fmt.Errorf("line %d: empty key", i+1)
		}
		if _, exists := values[key]; exists {
			return AutomationTOML{}, fmt.Errorf("duplicate key %q", key)
		}

		value := strings.TrimSpace(line[eq+1:])
		if strings.HasPrefix(value, `"`) {
			parsed, err := parseBasicString(value)
			if err != nil {
				return AutomationTOML{}, fmt.Errorf("key %q: %w", key, err)
			}
			values[key] = parsed
		} else {
			values[key] = value
			bare[key] = true
		}
	}

	for _, field := range []string{"id", "name", "status", "rrule", "target_thread_id"} {
		_, exists := values[field]
		if !exists {
			return AutomationTOML{}, fmt.Errorf("missing required field %q", field)
		}
		if bare[field] {
			return AutomationTOML{}, fmt.Errorf("field %q must be a quoted string, got bare value", field)
		}
	}

	return AutomationTOML{
		ID:             values["id"],
		Name:           values["name"],
		Status:         values["status"],
		Rrule:          values["rrule"],
		TargetThreadID: values["target_thread_id"],
	}, nil
}

// parseBasicStringはTOML basic stringのescapeを展開する。3重quoteやliteral stringは扱わない。
func parseBasicString(value string) (string, error) {
	if !strings.HasPrefix(value, `"`) {
		return "", fmt.Errorf("not a basic string")
	}

	var sb strings.Builder
	i := 1
	for i < len(value) {
		ch := value[i]
		if ch == '\\' {
			if i+1 >= len(value) {
				return "", fmt.Errorf("unterminated escape sequence")
			}
			next := value[i+1]
			switch next {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(next)
			}
			i += 2
			continue
		}
		if ch == '"' {
			remainder := strings.TrimSpace(value[i+1:])
			if remainder != "" && !strings.HasPrefix(remainder, "#") {
				return "", fmt.Errorf("trailing content after string close: %q", remainder)
			}
			return sb.String(), nil
		}
		sb.WriteByte(ch)
		i++
	}
	return "", fmt.Errorf("unterminated string")
}
