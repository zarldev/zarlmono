package service

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// ParameterType represents a JSON Schema type.
type ParameterType string

const (
	ParamString  ParameterType = "string"
	ParamNumber  ParameterType = "number"
	ParamBool    ParameterType = "boolean"
	ParamInteger ParameterType = "integer"
)

// Parameter describes a single tool parameter.
type Parameter struct {
	Name        string
	Type        ParameterType
	Description string
	Required    bool
	Enum        []string
}

// Parameters is a small builder for a tool's parameter schema. Tools keep
// their parameter lists in this readable form and call ToJSONSchema() to
// produce the llm.Schema that tools.ToolSpec.Parameters expects. (The
// tool/registry/result vocabulary itself lives in
// github.com/zarldev/zarlmono/zkit/ai/tools — this is only a schema helper.)
type Parameters []Parameter

// ToJSONSchema renders the parameter list as a typed llm.Schema.
func (p Parameters) ToJSONSchema() llm.Schema {
	properties := make(map[string]llm.Schema, len(p))
	var required []string

	for _, param := range p {
		prop := llm.Schema{
			Type:        string(param.Type),
			Description: param.Description,
		}
		if len(param.Enum) > 0 {
			enum := make([]any, len(param.Enum))
			for i, e := range param.Enum {
				enum[i] = e
			}
			prop.Enum = enum
		}
		properties[param.Name] = prop
		if param.Required {
			required = append(required, param.Name)
		}
	}

	return llm.Schema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}
