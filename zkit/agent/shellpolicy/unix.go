package shellpolicy

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// UnixParser walks a bash/sh AST via mvdan.cc/sh/v3 and emits a
// ParsedIR. The zero value is usable.
type UnixParser struct{}

// NewUnixParser returns a Unix adapter. The zero value is also fine;
// the constructor exists for symmetry with future Windows adapters.
func NewUnixParser() *UnixParser { return &UnixParser{} }

// Parse parses command as a Unix shell statement. Pure syntactic
// analysis — nothing is executed. Parse errors are surfaced both as
// a returned error and as a ReasonSyntaxError flag on the IR so the
// policy engine can fail closed without re-checking the error.
func (p *UnixParser) Parse(command string) (ParsedIR, error) {
	ir := emptyIR(PlatformUnix)

	parser := syntax.NewParser()
	f, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		ir.ParseErrors = append(ir.ParseErrors, err.Error())
		ir.RiskFlags = append(ir.RiskFlags, ReasonSyntaxError)
		return ir, fmt.Errorf("shellpolicy/unix: parse: %w", err)
	}

	seenCmd := map[string]bool{}
	seenOp := map[string]bool{}
	seenRisk := map[ReasonCode]bool{}
	seenFlag := map[string]map[string]bool{}

	// Two top-level statements mean an implicit ';' separator —
	// mvdan's walker doesn't surface that as a BinaryCmd, so we tag
	// it here.
	if len(f.Stmts) > 1 {
		addOperator(&ir, seenOp, ";")
		addRisk(&ir, seenRisk, ReasonOperator)
	}

	syntax.Walk(f, func(node syntax.Node) bool {
		if node == nil {
			return false
		}
		switch n := node.(type) {
		case *syntax.CallExpr:
			recordCall(&ir, seenCmd, seenFlag, seenRisk, n)
		case *syntax.BinaryCmd:
			addOperator(&ir, seenOp, n.Op.String())
			addRisk(&ir, seenRisk, ReasonOperator)
		case *syntax.Redirect:
			if isUnsafeRedirect(n) {
				addRisk(&ir, seenRisk, ReasonRedirect)
			}
		case *syntax.ParamExp, *syntax.ArithmExp:
			addRisk(&ir, seenRisk, ReasonExpansion)
		case *syntax.CmdSubst, *syntax.ProcSubst:
			addRisk(&ir, seenRisk, ReasonSubshell)
		}
		return true
	})

	return ir, nil
}

// recordCall extracts the canonical command key (tier-2 aware) and
// flag set from a CallExpr. Dynamic words that can't be statically
// resolved are skipped — they show up as an Expansion or Subshell
// risk flag emitted by the walker's other branches anyway.
func recordCall(
	ir *ParsedIR,
	seenCmd map[string]bool,
	seenFlag map[string]map[string]bool,
	seenRisk map[ReasonCode]bool,
	n *syntax.CallExpr,
) {
	if len(n.Args) == 0 {
		return
	}
	name, ok := resolveWord(n.Args[0])
	if !ok || name == "" || strings.Contains(name, "/") {
		return
	}

	key, argsStart := commandKey(name, n.Args)
	if !seenCmd[key] {
		seenCmd[key] = true
		ir.Commands = append(ir.Commands, key)
	}
	if name == "cd" {
		addRisk(ir, seenRisk, ReasonCd)
	}

	if _, ok := seenFlag[key]; !ok {
		seenFlag[key] = map[string]bool{}
	}
	for _, arg := range n.Args[argsStart:] {
		raw, ok := resolveWord(arg)
		if !ok || !strings.HasPrefix(raw, "-") {
			continue
		}
		flag := normalizeFlag(raw)
		if seenFlag[key][flag] {
			continue
		}
		seenFlag[key][flag] = true
		ir.CommandFlags[key] = append(ir.CommandFlags[key], flag)
	}
}

// commandKey returns the canonical command key and the index in
// args where flag scanning should begin. For tier-2 commands the
// key includes the first non-flag arg (the subcommand); for the
// rest it's just the binary name.
func commandKey(name string, args []*syntax.Word) (string, int) {
	if !tier2Commands[name] || len(args) < 2 {
		return name, 1
	}
	sub, ok := resolveWord(args[1])
	if !ok || sub == "" || strings.HasPrefix(sub, "-") {
		return name, 1
	}
	return name + " " + sub, 2
}

// resolveWord renders a Word into a string when all parts are
// statically resolvable (literals, single-quoted strings, and the
// literal parts of double-quoted strings). Returns ok=false the
// moment any dynamic part appears (expansion, subshell, etc.) so
// the caller can skip the word safely.
func resolveWord(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", true
	}
	var b strings.Builder
	for _, part := range w.Parts {
		if !appendPart(&b, part) {
			return "", false
		}
	}
	return b.String(), true
}

func appendPart(b *strings.Builder, p syntax.WordPart) bool {
	switch n := p.(type) {
	case *syntax.Lit:
		b.WriteString(n.Value)
		return true
	case *syntax.SglQuoted:
		b.WriteString(n.Value)
		return true
	case *syntax.DblQuoted:
		for _, qp := range n.Parts {
			if !appendPart(b, qp) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// normalizeFlag collapses --flag=value to --flag and any numeric-only
// flag (-1, -20) to -*. This keeps the IR's CommandFlags small and
// stable across calls that vary only in the numeric or value tail.
func normalizeFlag(raw string) string {
	if idx := strings.Index(raw, "="); idx != -1 {
		return raw[:idx]
	}
	if isNumericFlag(raw) {
		return "-*"
	}
	return raw
}

func isNumericFlag(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	body := s[1:]
	if len(body) > 1 && body[0] == '-' {
		return false
	}
	for _, r := range body {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isUnsafeRedirect reports whether a Redirect node represents output
// going to a real file. Reading (< file), writing to /dev/null, and
// fd merges (2>&1) are safe; everything else is treated as unsafe.
func isUnsafeRedirect(r *syntax.Redirect) bool {
	if r == nil {
		return false
	}
	switch r.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll:
		// fall through — these are real writes; check the target.
	default:
		// reads, heredocs, fd merges — not a write to a file.
		return false
	}
	target, ok := resolveWord(r.Word)
	if !ok {
		// Dynamic target — be conservative; the expansion-risk path
		// will catch the dynamic part separately.
		return true
	}
	switch target {
	case "/dev/null", "/dev/stdout", "/dev/stderr":
		return false
	}
	return true
}

func addRisk(ir *ParsedIR, seen map[ReasonCode]bool, code ReasonCode) {
	if seen[code] {
		return
	}
	seen[code] = true
	ir.RiskFlags = append(ir.RiskFlags, code)
}

func addOperator(ir *ParsedIR, seen map[string]bool, op string) {
	if op == "" || seen[op] {
		return
	}
	seen[op] = true
	ir.Operators = append(ir.Operators, op)
}
