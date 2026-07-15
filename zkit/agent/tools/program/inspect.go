package program

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"go.starlark.net/syntax"
)

// InspectedCall is a statically discovered program call site.
type InspectedCall struct {
	Name    tools.ToolName
	Args    tools.ToolParameters
	Dynamic bool
}

// Inspection is a best-effort static summary of a program script.
type Inspection struct {
	Calls   []InspectedCall
	Dynamic bool
}

// Inspect statically summarizes literal call/call_many sites in script. It is a
// UI hint only; runtime execution remains authoritative for dynamic Starlark.
func Inspect(script string) (Inspection, error) {
	file, err := syntax.LegacyFileOptions().Parse("program.star", script, 0)
	if err != nil {
		return Inspection{}, err
	}
	var out Inspection
	syntax.Walk(file, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		fn, ok := call.Fn.(*syntax.Ident)
		if !ok {
			return true
		}
		switch fn.Name {
		case builtinCall:
			out.Calls = append(out.Calls, inspectCallExpr(call))
		case builtinCallMany:
			calls, dynamic := inspectCallManyExpr(call)
			out.Calls = append(out.Calls, calls...)
			out.Dynamic = out.Dynamic || dynamic
		}
		return true
	})
	if len(out.Calls) == 0 && containsDynamicControl(file.Stmts) {
		out.Dynamic = true
	}
	return out, nil
}

func inspectCallExpr(call *syntax.CallExpr) InspectedCall {
	ic := InspectedCall{Dynamic: true}
	if len(call.Args) == 0 {
		return ic
	}
	if name, ok := literalString(call.Args[0]); ok {
		ic.Name = tools.ToolName(name)
		ic.Dynamic = false
	}
	if len(call.Args) > 1 {
		if args, ok := literalDict(call.Args[1]); ok {
			ic.Args = tools.ToolParameters(args)
		} else {
			ic.Dynamic = true
		}
	}
	return ic
}

func inspectCallManyExpr(call *syntax.CallExpr) ([]InspectedCall, bool) {
	if len(call.Args) == 0 {
		return nil, true
	}
	list, ok := call.Args[0].(*syntax.ListExpr)
	if !ok {
		return nil, true
	}
	out := make([]InspectedCall, 0, len(list.List))
	dynamic := false
	for _, elem := range list.List {
		entry, ok := literalDict(elem)
		if !ok {
			dynamic = true
			continue
		}
		ic := InspectedCall{}
		if name, ok := entry["name"].(string); ok && name != "" {
			ic.Name = tools.ToolName(name)
		} else {
			ic.Dynamic = true
			dynamic = true
		}
		if rawArgs, ok := entry["args"]; ok {
			if args, ok := rawArgs.(map[string]any); ok {
				ic.Args = tools.ToolParameters(args)
			} else {
				ic.Dynamic = true
				dynamic = true
			}
		}
		out = append(out, ic)
	}
	return out, dynamic
}

func literalDict(expr syntax.Expr) (map[string]any, bool) {
	dict, ok := expr.(*syntax.DictExpr)
	if !ok {
		return nil, false
	}
	out := make(map[string]any, len(dict.List))
	for _, elem := range dict.List {
		entry, ok := elem.(*syntax.DictEntry)
		if !ok {
			return nil, false
		}
		key, ok := literalString(entry.Key)
		if !ok {
			return nil, false
		}
		val, ok := literalValue(entry.Value)
		if !ok {
			return nil, false
		}
		out[key] = val
	}
	return out, true
}

func literalValue(expr syntax.Expr) (any, bool) {
	switch x := expr.(type) {
	case *syntax.Literal:
		switch v := x.Value.(type) {
		case string, int64, float64:
			return v, true
		default:
			return fmt.Sprint(v), true
		}
	case *syntax.Ident:
		switch x.Name {
		case "true":
			return true, true
		case "false":
			return false, true
		case "None", "none", "null":
			return nil, true
		}
	case *syntax.DictExpr:
		return literalDict(x)
	case *syntax.ListExpr:
		vals := make([]any, 0, len(x.List))
		for _, elem := range x.List {
			v, ok := literalValue(elem)
			if !ok {
				return nil, false
			}
			vals = append(vals, v)
		}
		return vals, true
	}
	return nil, false
}

func literalString(expr syntax.Expr) (string, bool) {
	lit, ok := expr.(*syntax.Literal)
	if !ok {
		return "", false
	}
	s, ok := lit.Value.(string)
	return s, ok
}

func containsDynamicControl(stmts []syntax.Stmt) bool {
	for _, stmt := range stmts {
		switch stmt.(type) {
		case *syntax.ForStmt, *syntax.WhileStmt, *syntax.IfStmt:
			return true
		}
	}
	return false
}
