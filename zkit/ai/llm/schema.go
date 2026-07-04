package llm

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
)

// Schema is the typed JSON-Schema fragment describing a tool's parameters.
// It replaces the old map[string]any so required/enum have exactly one Go
// type each (no []string-vs-[]any reconciliation at every consumer), while
// Extra preserves the open-world JSON-Schema keys (oneOf, format, minimum,
// $ref, patternProperties, …) that MCP servers and hand-written schemas send
// straight to the model — they must round-trip untouched.
//
// All the JSON<->Go juggling lives in (Un)MarshalJSON / SchemaFromMap, so
// every other producer and consumer works in typed fields.
type Schema struct {
	Type        string
	Description string
	// Properties are stored by value — map entries aren't addressable, so a
	// pointer would only add nil checks and GC pressure with no aliasing win.
	Properties map[string]Schema
	Required   []string
	Enum       []any
	// Items is a pointer because a struct can't embed itself by value
	// (infinite size); nil means "no items constraint".
	Items *Schema
	// AdditionalProperties is bool | Schema (JSON Schema permits either).
	AdditionalProperties any
	// Extra carries every JSON-Schema key not modelled above, preserved
	// verbatim so externally-sourced schemas reach the model losslessly.
	Extra map[string]any

	// PropertyOrder fixes the order Properties serialize in. Property order
	// is load-bearing for grammar-constrained sampling: llama.cpp's
	// schema-to-GBNF converter emits properties in document order, so a
	// schema that wants a free-text rationale generated BEFORE an enum
	// commitment must serialize rationale first. Without this, MarshalJSON
	// falls back to Go's map marshalling — alphabetical — which silently
	// reorders ("action" before "rationale"). Names not present in
	// Properties are skipped; properties not named here follow in sorted
	// order so none are dropped. Ignored by Map(), which cannot carry order.
	PropertyOrder []string
}

const (
	schemaKeyType                 = "type"
	schemaKeyDescription          = "description"
	schemaKeyProperties           = "properties"
	schemaKeyRequired             = "required"
	schemaKeyEnum                 = "enum"
	schemaKeyItems                = "items"
	schemaKeyAdditionalProperties = "additionalProperties"
)

// SchemaFromMap builds a Schema from a generic JSON-Schema map — the ingest
// path for MCP servers, dynamic-tool --describe output, and persisted catalog
// entries, where the schema arrives via json.Unmarshal (so required/enum are
// []any). A nil map yields the zero Schema.
func SchemaFromMap(m map[string]any) Schema {
	var s Schema
	for k, v := range m {
		switch k {
		case schemaKeyType:
			s.Type, _ = v.(string)
		case schemaKeyDescription:
			s.Description, _ = v.(string)
		case schemaKeyRequired:
			s.Required = toStringSlice(v)
		case schemaKeyEnum:
			s.Enum = toAnySlice(v)
		case schemaKeyProperties:
			if pm, ok := v.(map[string]any); ok {
				s.Properties = make(map[string]Schema, len(pm))
				for pk, pv := range pm {
					if sub, ok := pv.(map[string]any); ok {
						s.Properties[pk] = SchemaFromMap(sub)
					}
				}
			}
		case schemaKeyItems:
			if im, ok := v.(map[string]any); ok {
				sub := SchemaFromMap(im)
				s.Items = &sub
			}
		case schemaKeyAdditionalProperties:
			switch av := v.(type) {
			case bool:
				s.AdditionalProperties = av
			case map[string]any:
				s.AdditionalProperties = SchemaFromMap(av)
			default:
				s.AdditionalProperties = v
			}
		default:
			if s.Extra == nil {
				s.Extra = map[string]any{}
			}
			s.Extra[k] = v
		}
	}
	return s
}

// IsZero reports whether the schema carries no constraints — the no-arguments
// case. Used where the old map representation was checked for nil/len==0.
func (s Schema) IsZero() bool {
	return s.Type == "" && s.Description == "" && len(s.Properties) == 0 &&
		len(s.Required) == 0 && len(s.Enum) == 0 && s.Items == nil &&
		s.AdditionalProperties == nil && len(s.Extra) == 0
}

// toMap renders the schema back to a generic JSON-Schema map. Extra is laid
// down first so the typed keys always win on the (illegal) chance of overlap.
func (s Schema) toMap() map[string]any {
	m := make(map[string]any, len(s.Extra)+6)
	maps.Copy(m, s.Extra)
	if s.Type != "" {
		m[schemaKeyType] = s.Type
	}
	if s.Description != "" {
		m[schemaKeyDescription] = s.Description
	}
	// Emit when non-nil even if empty: an object tool schema with no fields
	// is "properties":{} (a non-nil empty map), distinct from the zero Schema
	// which has nil Properties and omits the key.
	if s.Properties != nil {
		pm := make(map[string]any, len(s.Properties))
		for pk, pv := range s.Properties {
			pm[pk] = pv.toMap()
		}
		m[schemaKeyProperties] = pm
	}
	if len(s.Required) > 0 {
		m[schemaKeyRequired] = s.Required
	}
	if len(s.Enum) > 0 {
		m[schemaKeyEnum] = s.Enum
	}
	if s.Items != nil {
		m[schemaKeyItems] = s.Items.toMap()
	}
	switch av := s.AdditionalProperties.(type) {
	case nil:
		// absent
	case Schema:
		m[schemaKeyAdditionalProperties] = av.toMap()
	case *Schema:
		if av != nil {
			m[schemaKeyAdditionalProperties] = av.toMap()
		}
	default:
		m[schemaKeyAdditionalProperties] = av
	}
	return m
}

// Map returns the schema as a generic JSON-Schema map for the few consumers
// (e.g. SDKs whose field type is map[string]any) that still need one.
func (s Schema) Map() map[string]any { return s.toMap() }

// MarshalJSON uses a value receiver so json also invokes it for the
// non-addressable Schema values inside Properties. When PropertyOrder is
// set, properties are emitted in that order (see the field doc — grammar
// converters read document order); otherwise the map path applies and
// keys serialize alphabetically.
func (s Schema) MarshalJSON() ([]byte, error) {
	m := s.toMap()
	if len(s.PropertyOrder) == 0 || len(s.Properties) == 0 {
		return json.Marshal(m)
	}
	delete(m, schemaKeyProperties)
	base, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(`{"properties":{`)
	seen := make(map[string]bool, len(s.Properties))
	first := true
	writeProp := func(name string) error {
		sub, ok := s.Properties[name]
		if !ok || seen[name] {
			return nil
		}
		seen[name] = true
		val, err := json.Marshal(sub) // recurses, honouring nested order
		if err != nil {
			return err
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		key, err := json.Marshal(name)
		if err != nil {
			return err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(val)
		return nil
	}
	for _, name := range s.PropertyOrder {
		if err := writeProp(name); err != nil {
			return nil, err
		}
	}
	rest := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	for _, name := range rest {
		if err := writeProp(name); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	if len(base) > 2 { // base == "{}" when properties was the only key
		buf.WriteByte(',')
		buf.Write(base[1 : len(base)-1])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON funnels through the generic map so a single code path handles
// both the []string (in-process literals) and []any (post-json) shapes.
func (s *Schema) UnmarshalJSON(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*s = SchemaFromMap(m)
	return nil
}

func toStringSlice(v any) []string {
	switch a := v.(type) {
	case []string:
		return a
	case []any:
		out := make([]string, 0, len(a))
		for _, e := range a {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func toAnySlice(v any) []any {
	switch a := v.(type) {
	case []any:
		return a
	case []string:
		out := make([]any, len(a))
		for i, str := range a {
			out[i] = str
		}
		return out
	}
	return nil
}
