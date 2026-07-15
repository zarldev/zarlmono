package program

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"go.starlark.net/starlark"
)

const maxConvertDepth = 64

func normalizeJSON(v any, maxBytes int) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && len(b) > maxBytes {
		return nil, fmt.Errorf("value exceeds %d bytes", maxBytes)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if err := validateJSONValue(out, 0); err != nil {
		return nil, err
	}
	return out, nil
}
func validateJSONValue(v any, depth int) error {
	if depth > maxConvertDepth {
		return fmt.Errorf("value exceeds max depth %d", maxConvertDepth)
	}
	switch x := v.(type) {
	case nil, bool, string:
		return nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return nil
	case float32:
		if math.IsInf(float64(x), 0) || math.IsNaN(float64(x)) {
			return errors.New("non-finite float")
		}
		return nil
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return errors.New("non-finite float")
		}
		return nil
	case []any:
		for _, elem := range x {
			if err := validateJSONValue(elem, depth+1); err != nil {
				return err
			}
		}
		return nil
	case []string:
		return nil
	case map[string]any:
		for _, elem := range x {
			if err := validateJSONValue(elem, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported JSON value type %T", v)
	}
}

func toStarlark(v any) (starlark.Value, error) {
	switch x := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(x), nil
	case string:
		return starlark.String(x), nil
	case int:
		return starlark.MakeInt(x), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return nil, errors.New("non-finite float")
		}
		return starlark.Float(x), nil
	case []any:
		elems := make([]starlark.Value, 0, len(x))
		for _, elem := range x {
			v, err := toStarlark(elem)
			if err != nil {
				return nil, err
			}
			elems = append(elems, v)
		}
		return starlark.NewList(elems), nil
	case map[string]any:
		d := starlark.NewDict(len(x))
		for k, elem := range x {
			v, err := toStarlark(elem)
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), v); err != nil {
				return nil, err
			}
		}
		return d, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}

func fromStarlark(v starlark.Value) (any, error) {
	return fromStarlarkDepth(v, 0)
}

func fromStarlarkDepth(v starlark.Value, depth int) (any, error) {
	if depth > maxConvertDepth {
		return nil, fmt.Errorf("value exceeds max depth %d", maxConvertDepth)
	}
	switch x := v.(type) {
	case starlark.NoneType:
		var out any
		return out, nil
	case starlark.Bool:
		return bool(x), nil
	case starlark.String:
		s, err := strconv.Unquote(x.String())
		if err != nil {
			return nil, err
		}
		return s, nil
	case starlark.Int:
		if i, ok := x.Int64(); ok {
			return i, nil
		}
		return nil, errors.New("integer out of int64 range")
	case starlark.Float:
		f := float64(x)
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, errors.New("non-finite float")
		}
		return f, nil
	case *starlark.List:
		out := make([]any, 0, x.Len())
		for i := range x.Len() {
			elem, err := fromStarlarkDepth(x.Index(i), depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, elem)
		}
		return out, nil
	case starlark.Tuple:
		out := make([]any, 0, x.Len())
		for i := range x.Len() {
			elem, err := fromStarlarkDepth(x.Index(i), depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, elem)
		}
		return out, nil
	case *starlark.Dict:
		out := make(map[string]any, x.Len())
		for _, item := range x.Items() {
			k, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("dictionary key %s is not a string", item[0].Type())
			}
			key, err := strconv.Unquote(k.String())
			if err != nil {
				return nil, err
			}
			elem, err := fromStarlarkDepth(item[1], depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = elem
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported Starlark value type %s", v.Type())
	}
}
