package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
)

const schemaHeaderAnnotation = "x-mcp-header"

var maxSchemaHeaderInteger = func() *big.Int {
	value := new(big.Int).Lsh(big.NewInt(1), 53)
	return value.Sub(value, big.NewInt(1))
}()

type schemaHeaderBinding struct {
	Name string
	Path []string
	Type string
}

func schemaHeaderBindings(schema map[string]any) ([]schemaHeaderBinding, error) {
	seen := make(map[string]bool)
	var bindings []schemaHeaderBinding
	if err := collectSchemaHeaderBindings(schema, nil, false, seen, &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

func collectSchemaHeaderBindings(schema map[string]any, path []string, property bool, seen map[string]bool, bindings *[]schemaHeaderBinding) error {
	annotation, annotated := schema[schemaHeaderAnnotation]
	if annotated {
		if !property || hasForbiddenHeaderShape(schema) {
			return errors.New("x-mcp-header is only allowed on primitive properties")
		}
		name, ok := annotation.(string)
		if !ok || !validHTTPToken(name) {
			return errors.New("x-mcp-header must be an RFC token")
		}
		typeName, ok := schema["type"].(string)
		if !ok || (typeName != "string" && typeName != "boolean" && typeName != "integer") {
			return errors.New("x-mcp-header property must be a string, boolean, or integer")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("duplicate x-mcp-header %q", name)
		}
		seen[key] = true
		*bindings = append(*bindings, schemaHeaderBinding{Name: name, Path: append([]string(nil), path...), Type: typeName})
	}

	for key, value := range schema {
		if key == "properties" {
			properties, ok := value.(map[string]any)
			if !ok {
				if containsSchemaHeader(value) {
					return errors.New("x-mcp-header must be under a properties object")
				}
				continue
			}
			for name, rawProperty := range properties {
				propertySchema, ok := rawProperty.(map[string]any)
				if !ok {
					if containsSchemaHeader(rawProperty) {
						return errors.New("x-mcp-header property schema must be an object")
					}
					continue
				}
				if err := collectSchemaHeaderBindings(propertySchema, append(path, name), true, seen, bindings); err != nil {
					return err
				}
			}
			continue
		}
		if key != schemaHeaderAnnotation && containsSchemaHeader(value) {
			return fmt.Errorf("x-mcp-header is not allowed under %q", key)
		}
	}
	return nil
}

func hasForbiddenHeaderShape(schema map[string]any) bool {
	if typeName, _ := schema["type"].(string); typeName == "array" {
		return true
	}
	for _, key := range []string{"items", "prefixItems", "allOf", "anyOf", "oneOf", "not", "if", "then", "else", "$ref"} {
		if _, present := schema[key]; present {
			return true
		}
	}
	return false
}

func containsSchemaHeader(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, present := value[schemaHeaderAnnotation]; present {
			return true
		}
		for _, nested := range value {
			if containsSchemaHeader(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range value {
			if containsSchemaHeader(nested) {
				return true
			}
		}
	}
	return false
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for i := range len(value) {
		character := value[i]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func schemaHeaderValue(arguments json.RawMessage, binding schemaHeaderBinding) (string, bool, error) {
	current := arguments
	for _, field := range binding.Path {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return "", false, fmt.Errorf("header-bound argument path is not an object: %w", err)
		}
		var present bool
		current, present = object[field]
		if !present || bytes.Equal(bytes.TrimSpace(current), []byte("null")) {
			return "", false, nil
		}
	}

	switch binding.Type {
	case "string":
		var value string
		if err := json.Unmarshal(current, &value); err != nil {
			return "", false, errors.New("header-bound argument must be a string")
		}
		return value, true, nil
	case "boolean":
		var value bool
		if err := json.Unmarshal(current, &value); err != nil {
			return "", false, errors.New("header-bound argument must be a boolean")
		}
		if value {
			return "true", true, nil
		}
		return "false", true, nil
	case "integer":
		var number json.Number
		decoder := json.NewDecoder(bytes.NewReader(current))
		decoder.UseNumber()
		if err := decoder.Decode(&number); err != nil {
			return "", false, errors.New("header-bound argument must be an integer")
		}
		rational, ok := new(big.Rat).SetString(number.String())
		if !ok || !rational.IsInt() || new(big.Int).Abs(rational.Num()).Cmp(maxSchemaHeaderInteger) > 0 {
			return "", false, errors.New("header-bound integer is outside the safe range")
		}
		return rational.Num().String(), true, nil
	default:
		return "", false, errors.New("unsupported header-bound argument type")
	}
}

func applySchemaHeaders(header http.Header, arguments json.RawMessage, bindings []schemaHeaderBinding) error {
	for _, binding := range bindings {
		value, present, err := schemaHeaderValue(arguments, binding)
		if err != nil {
			return err
		}
		if present {
			header.Set(headerParameterPrefix+binding.Name, encodeProtocolHeader(value))
		}
	}
	return nil
}

func verifySchemaHeaders(header http.Header, arguments json.RawMessage, bindings []schemaHeaderBinding) error {
	for _, binding := range bindings {
		name := headerParameterPrefix + binding.Name
		values := header.Values(name)
		expected, present, err := schemaHeaderValue(arguments, binding)
		if err != nil {
			return err
		}
		if !present {
			if len(values) != 0 {
				return errors.New("unexpected MCP parameter header")
			}
			continue
		}
		if len(values) != 1 {
			return errors.New("missing or repeated MCP parameter header")
		}
		actual, err := decodeProtocolHeader(values[0])
		if err != nil || actual != expected {
			return errors.New("MCP parameter header mismatch")
		}
	}
	return nil
}
