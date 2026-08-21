package packet

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeSchema(t *testing.T, encoded string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("schema json: %v", err)
	}
	return decoded
}

func TestSchemaJSONRestrictedVocabulary(t *testing.T) {
	allowedNodeKeys := map[string]bool{"type": true, "properties": true, "required": true, "enum": true, "items": true}
	allowedTypes := map[string]bool{"object": true, "array": true, "string": true, "number": true, "boolean": true}

	for name, encoded := range map[string]func() (string, error){
		"worker":   WorkerSchemaJSON,
		"reviewer": ReviewerSchemaJSON,
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := encoded()
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			decoded := decodeSchema(t, encoded)
			if decoded["type"] != "object" {
				t.Fatalf("root type = %v", decoded["type"])
			}
			var walk func(node map[string]any, path string)
			walk = func(node map[string]any, path string) {
				for key := range node {
					if !allowedNodeKeys[key] {
						t.Fatalf("%s: vocabulary外のkey %q", path, key)
					}
				}
				if nodeType, _ := node["type"].(string); !allowedTypes[nodeType] {
					t.Fatalf("%s: 許可外type %q", path, nodeType)
				}
				switch node["type"] {
				case "object":
					properties, _ := node["properties"].(map[string]any)
					if len(properties) == 0 {
						t.Fatalf("%s: objectにpropertiesがありません", path)
					}
					for name, raw := range properties {
						child, _ := raw.(map[string]any)
						walk(child, path+"."+name)
					}
					if required, ok := node["required"].([]any); ok {
						for _, raw := range required {
							name, _ := raw.(string)
							if _, ok := properties[name]; !ok {
								t.Fatalf("%s: required %qがpropertiesにありません", path, name)
							}
						}
					}
				case "array":
					items, _ := node["items"].(map[string]any)
					if items == nil {
						t.Fatalf("%s: arrayにitemsがありません", path)
					}
					walk(items, path+".items")
				}
			}
			walk(decoded, "$")
		})
	}
}

func TestWorkerSchemaContents(t *testing.T) {
	encoded, err := WorkerSchemaJSON()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	decoded := decodeSchema(t, encoded)
	properties := decoded["properties"].(map[string]any)
	status := properties["status"].(map[string]any)
	enum := status["enum"].([]any)
	values := make([]string, 0, len(enum))
	for _, raw := range enum {
		values = append(values, raw.(string))
	}
	if strings.Join(values, ",") != "IMPLEMENTED,NEEDS_SOL_DECISION" {
		t.Fatalf("worker status enum = %v", values)
	}
	required := decoded["required"].([]any)
	if len(required) != 4 {
		t.Fatalf("required = %v", required)
	}
	for _, want := range []string{"status", "risk", "targets", "artifacts"} {
		found := false
		for _, raw := range required {
			if raw == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("worker requiredに%sがありません: %v", want, required)
		}
	}
	for _, want := range []string{"decision", "evidence", "options", "recommendation", "test_obligations", "targets", "artifacts"} {
		if _, ok := properties[want]; !ok {
			t.Fatalf("worker schemaに%sがありません", want)
		}
	}
}

func TestReviewerSchemaContents(t *testing.T) {
	encoded, err := ReviewerSchemaJSON()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	decoded := decodeSchema(t, encoded)
	properties := decoded["properties"].(map[string]any)
	status := properties["status"].(map[string]any)
	enum := status["enum"].([]any)
	values := make([]string, 0, len(enum))
	for _, raw := range enum {
		values = append(values, raw.(string))
	}
	if strings.Join(values, ",") != "PASS,FIX_REQUIRED,NEEDS_SOL_REVIEW" {
		t.Fatalf("reviewer status enum = %v", values)
	}
	for _, want := range []string{"invariants", "test_evidence", "issues", "residual_risk", "sol_question", "targets", "artifacts"} {
		if _, ok := properties[want]; !ok {
			t.Fatalf("reviewer schemaに%sがありません", want)
		}
	}
	if _, ok := properties["decision"]; ok {
		t.Fatal("reviewer schemaはworker専用fieldを持ってはいけません")
	}
}

func TestSchemaValidationPanicsOnVocabularyViolation(t *testing.T) {
	cases := []struct {
		name   string
		schema *objectSchema
	}{
		{"unknown scalar type", &objectSchema{
			Type: "object",
			Properties: map[string]*propertySchema{
				"x": {scalar: &scalarSchema{Type: "integer"}},
			},
		}},
		{"enum on non string", &objectSchema{
			Type: "object",
			Properties: map[string]*propertySchema{
				"x": {scalar: &scalarSchema{Type: schemaTypeNumber, Enum: []string{"1"}}},
			},
		}},
		{"enum duplicate", &objectSchema{
			Type: "object",
			Properties: map[string]*propertySchema{
				"x": {scalar: &scalarSchema{Type: schemaTypeString, Enum: []string{"a", "a"}}},
			},
		}},
		{"enum empty value", &objectSchema{
			Type: "object",
			Properties: map[string]*propertySchema{
				"x": {scalar: &scalarSchema{Type: schemaTypeString, Enum: []string{""}}},
			},
		}},
		{"required not in properties", &objectSchema{
			Type: "object",
			Properties: map[string]*propertySchema{
				"x": stringProperty(),
			},
			Required: []string{"y"},
		}},
		{"array items enum", &objectSchema{
			Type: "object",
			Properties: map[string]*propertySchema{
				"x": {array: &arraySchema{Type: schemaTypeArray, Items: scalarSchema{Type: schemaTypeString, Enum: []string{"a"}}}},
			},
		}},
		{"empty property", &objectSchema{
			Type: "object",
			Properties: map[string]*propertySchema{
				"x": {},
			},
		}},
		{"nested object violation", &objectSchema{
			Type: "object",
			Properties: map[string]*propertySchema{
				"x": {object: &objectSchema{
					Type:       "object",
					Properties: map[string]*propertySchema{"y": {scalar: &scalarSchema{Type: "integer"}}},
				}},
			},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			_, err := schemaJSON(c.schema)
			if err != nil {
				t.Fatalf("unexpected error before validation: %v", err)
			}
		})
	}
}

func TestSchemaRoundTripViaUnmarshal(t *testing.T) {
	encoded, err := WorkerSchemaJSON()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("err = %v", err)
	}
	if decoded["type"] != "object" || decoded["properties"] == nil || decoded["required"] == nil {
		t.Fatalf("schema root shape: %v", decoded)
	}
}
