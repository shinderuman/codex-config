package packet

import (
	"encoding/json"
	"fmt"
	"sort"
)

// schema vocabularyはClaude CLIのstructured output実測(PoC)で成立を確認した
// object root・properties・required・enum・array・items・boolean・string・numberだけに
// 事前制限する。未検証のmaxLength/additionalProperties・composition(allOf/anyOf/oneOf/not)
// へ依存するとCLI内部のretry枯渇までprovider消費が進むため、ここで構築時に拒否する。
const (
	schemaTypeObject  = "object"
	schemaTypeArray   = "array"
	schemaTypeString  = "string"
	schemaTypeNumber  = "number"
	schemaTypeBoolean = "boolean"
)

// scalarTypesは末端schemaに許可するtypeの白list。
var scalarTypes = map[string]struct{}{
	schemaTypeString:  {},
	schemaTypeNumber:  {},
	schemaTypeBoolean: {},
}

// scalarSchemaはproperties値とarray itemsに使える末端schema。
type scalarSchema struct {
	Type string   `json:"type"`
	Enum []string `json:"enum,omitempty"`
}

type objectSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]*propertySchema `json:"properties"`
	Required   []string                   `json:"required"`
}

type arraySchema struct {
	Type  string       `json:"type"`
	Items scalarSchema `json:"items"`
}

type propertySchema struct {
	scalar *scalarSchema
	array  *arraySchema
	object *objectSchema
}

// MarshalJSONはpropertySchemaを末端表現へ直列化する。
func (p propertySchema) MarshalJSON() ([]byte, error) {
	switch {
	case p.scalar != nil:
		return json.Marshal(p.scalar)
	case p.array != nil:
		return json.Marshal(p.array)
	case p.object != nil:
		return json.Marshal(p.object)
	default:
		return nil, fmt.Errorf("property schemaの中身が空です")
	}
}

func stringProperty(values ...string) *propertySchema {
	if len(values) == 0 {
		return &propertySchema{scalar: &scalarSchema{Type: schemaTypeString}}
	}
	return &propertySchema{scalar: &scalarSchema{Type: schemaTypeString, Enum: values}}
}

func stringsProperty() *propertySchema {
	return &propertySchema{array: &arraySchema{Type: schemaTypeArray, Items: scalarSchema{Type: schemaTypeString}}}
}

func riskProperty() *propertySchema {
	return stringProperty(string(RiskLow), string(RiskHigh))
}

// workerSchemaはworker role呼出(worker new/decision/fix/report-only/resume)のschema。
// 必須はschemaで表現できる共通最小限だけとし、status別必須field・risk整合は
// ValidateWorkerResultが強制する。
func workerSchema() *objectSchema {
	return &objectSchema{
		Type: schemaTypeObject,
		Properties: map[string]*propertySchema{
			"status":               stringProperty(string(StatusImplemented), string(StatusNeedsSolDecision)),
			"risk":                 riskProperty(),
			"summary":              stringProperty(),
			"requirement_coverage": stringProperty(),
			"tests":                stringProperty(),
			"unverified":           stringProperty(),
			"decision":             stringProperty(),
			"evidence":             stringProperty(),
			"options":              stringProperty(),
			"recommendation":       stringProperty(),
			"test_obligations":     stringProperty(),
			"targets":              stringsProperty(),
			"artifacts":            stringsProperty(),
		},
		Required: []string{"status", "risk", "artifacts"},
	}
}

// reviewerSchemaは独立reviewer・risk floor再出力呼出のschema。
func reviewerSchema() *objectSchema {
	return &objectSchema{
		Type: schemaTypeObject,
		Properties: map[string]*propertySchema{
			"status":               stringProperty(string(StatusPass), string(StatusFixRequired), string(StatusNeedsSolReview)),
			"risk":                 riskProperty(),
			"summary":              stringProperty(),
			"requirement_coverage": stringProperty(),
			"invariants":           stringProperty(),
			"test_evidence":        stringProperty(),
			"issues":               stringProperty(),
			"residual_risk":        stringProperty(),
			"sol_question":         stringProperty(),
			"targets":              stringsProperty(),
			"artifacts":            stringsProperty(),
		},
		Required: []string{"status", "risk", "artifacts"},
	}
}

// WorkerSchemaJSONは検証済みworker schemaのJSON文字列を返す。
func WorkerSchemaJSON() (string, error) {
	return schemaJSON(workerSchema())
}

// ReviewerSchemaJSONは検証済みreviewer schemaのJSON文字列を返す。
func ReviewerSchemaJSON() (string, error) {
	return schemaJSON(reviewerSchema())
}

func schemaJSON(schema *objectSchema) (string, error) {
	validateObjectSchema(schema, "$")
	data, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// validateObjectSchemaはschema vocabularyを制限内へ強制する。
// 構築元は自packageの固定値だが、schema追加・変更時に未検証keywordや許可外typeへ
// 依存した構成が混入したら呼出前に失敗させ、provider消費済みのretry枯渇へ至らせない。
func validateObjectSchema(schema *objectSchema, path string) {
	if schema == nil || schema.Type != schemaTypeObject {
		panic(fmt.Sprintf("%s: object schemaのtypeがobjectではありません", path))
	}
	if len(schema.Properties) == 0 {
		panic(fmt.Sprintf("%s: object schemaにpropertiesがありません", path))
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		validatePropertySchema(schema.Properties[name], path+"."+name)
	}
	for _, required := range schema.Required {
		if _, ok := schema.Properties[required]; !ok {
			panic(fmt.Sprintf("%s: requiredの%sがpropertiesにありません", path, required))
		}
	}
}

func validatePropertySchema(property *propertySchema, path string) {
	switch {
	case property.scalar != nil:
		validateScalarSchema(property.scalar, path)
	case property.array != nil:
		if property.array.Type != schemaTypeArray {
			panic(fmt.Sprintf("%s: array schemaのtypeがarrayではありません", path))
		}
		if len(property.array.Items.Enum) != 0 {
			panic(fmt.Sprintf("%s: array itemsへenumを指定できません", path))
		}
		validateScalarSchema(&property.array.Items, path+".items")
	case property.object != nil:
		validateObjectSchema(property.object, path)
	default:
		panic(fmt.Sprintf("%s: property schemaの中身が空です", path))
	}
}

func validateScalarSchema(schema *scalarSchema, path string) {
	if _, ok := scalarTypes[schema.Type]; !ok {
		panic(fmt.Sprintf("%s: scalar type %qは許可list外です", path, schema.Type))
	}
	if len(schema.Enum) == 0 {
		return
	}
	if schema.Type != schemaTypeString {
		panic(fmt.Sprintf("%s: enumはstring以外へ指定できません", path))
	}
	seen := make(map[string]struct{}, len(schema.Enum))
	for _, value := range schema.Enum {
		if value == "" {
			panic(fmt.Sprintf("%s: enumに空文字が含まれます", path))
		}
		if _, ok := seen[value]; ok {
			panic(fmt.Sprintf("%s: enum %sが重複しています", path, value))
		}
		seen[value] = struct{}{}
	}
}
