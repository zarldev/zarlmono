package shellpolicy

import (
	"errors"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ErrUnparseable reports that a command could not be parsed as a shell
// statement, so no write targets could be extracted from it. Callers match it
// with errors.Is to distinguish "parsed, found nothing to write" from "could
// not analyse" — the [TestEditStrictGuardrail] treats the latter as a pass and
// leaves the syntax rejection to the ShellGuardrail.
var ErrUnparseable = errors.New("shellpolicy: unparseable command")

// contentMutators are commands that create, overwrite, truncate, relocate, or
// delete file CONTENT. Permission-only commands (chmod / chown) are
// deliberately excluded — they can't alter a grader's test assertions, so
// gating them would only add false rejections. Used by [WriteTargets] to
// decide which CallExpr operands are write targets.
var contentMutators = map[string]bool{
	"rm":       true,
	"unlink":   true,
	"mv":       true,
	"cp":       true,
	"tee":      true,
	"truncate": true,
	"ln":       true,
	"install":  true,
}

// gitMutatingSubcommands are the `git <sub>` forms that remove, move, or
// overwrite working-tree files (the ways a model could erase or revert a
// grader's tests through git rather than the filesystem).
var gitMutatingSubcommands = map[string]bool{
	"rm":       true,
	"mv":       true,
	"checkout": true,
	"restore":  true,
}

// gitArgTakingGlobalFlags are git's pre-subcommand global options that
// consume a following token (`git -C dir rm …`). Without modelling these,
// the subcommand scan mistakes the flag's argument for the subcommand and a
// real `git -C . rm foo_test.go` slips through with no write targets.
var gitArgTakingGlobalFlags = map[string]bool{
	"-C":             true,
	"-c":             true,
	"--git-dir":      true,
	"--work-tree":    true,
	"--namespace":    true,
	"--exec-path":    true,
	"--super-prefix": true,
	"--config-env":   true,
}

// transparentWrappers run another command, so the real mutator hides one
// argument deeper: `env rm x`, `command rm x`, `xargs rm x`, `busybox rm x`,
// `sudo rm x`, `timeout 5 rm x`. WriteTargets recurses through them to the
// effective command.
var transparentWrappers = map[string]bool{
	"command": true,
	"env":     true,
	"xargs":   true,
	"nice":    true,
	"nohup":   true,
	"setsid":  true,
	"stdbuf":  true,
	"sudo":    true,
	"doas":    true,
	"time":    true,
	"timeout": true,
	"busybox": true,
}

// findPatternPredicates name the operand that follows them (the glob/regex a
// `find … -delete` would match), so `find . -name '*_test.go' -delete` yields
// `*_test.go` as a target — which a test-path matcher recognises.
var findPatternPredicates = map[string]bool{
	"-name":      true,
	"-iname":     true,
	"-path":      true,
	"-ipath":     true,
	"-wholename": true,
	"-regex":     true,
	"-iregex":    true,
}

// maxWrapperDepth bounds wrapper recursion (`env env env rm …`) so a
// pathological command can't blow the stack.
const maxWrapperDepth = 8

// WriteTargets statically extracts the file paths a shell command would
// CREATE, OVERWRITE, TRUNCATE, MOVE, or DELETE. It is pure syntactic analysis
// via mvdan.cc/sh/v3 — nothing is executed. It exists so a guardrail can screen
// model-issued shell against the same test-file protection the write-style tool
// guards enforce; bash is otherwise a free bypass of those guards.
//
// Coverage:
//   - content mutators (rm, unlink, mv, cp, tee, truncate, ln, install):
//     every statically-resolvable non-flag operand.
//   - in-place stream editors (sed, perl): the file operands — skipping the
//     leading script word — but only when an -i flag is present (without it
//     these only read).
//   - dd: the of=PATH operand only (if=… is a read).
//   - git rm / mv / checkout / restore: operands after the subcommand.
//   - output redirects (>, >>, and friends): the target file (read redirects,
//     /dev/null, and fd merges are ignored, matching [isUnsafeRedirect]).
//
// CallExprs nested in subshells, command substitutions, and && / ; chains are
// walked too, so `(cd x && rm y_test.go)` and `$(rm y_test.go)` are covered.
// The command name is matched on its basename, so `/bin/rm` is screened like
// `rm`.
//
// Words with dynamic parts (parameter expansion, subshell output) can't be
// resolved statically and are skipped — a full [UnixParser.Parse] of the same
// command raises Expansion/Subshell risk flags a caller can fail closed on
// separately. False positives (a non-path operand that happens to look like a
// path) are acceptable: the consumer surfaces a clear rejection and the agent
// can route around it.
//
// An unparseable command returns no targets and [ErrUnparseable] (wrapping the
// parse error) so the caller can tell it apart from a clean "nothing to write."
func WriteTargets(command string) ([]string, error) {
	parser := syntax.NewParser()
	f, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnparseable, err)
	}

	var targets []string
	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			targets = append(targets, callWriteTargets(n)...)
		case *syntax.Redirect:
			if !isUnsafeRedirect(n) {
				return true
			}
			if t, ok := resolveWord(n.Word); ok && t != "" {
				targets = append(targets, t)
			}
		}
		return true
	})
	return targets, nil
}

// callWriteTargets returns the write-target operands of a single command
// invocation, or nil for commands that don't mutate file content.
func callWriteTargets(n *syntax.CallExpr) []string {
	if len(n.Args) == 0 {
		return nil
	}
	return dispatchWriteTargets(n.Args, 0)
}

// dispatchWriteTargets resolves the command head from args[0] and extracts its
// write targets, recursing through transparent wrappers up to maxWrapperDepth.
func dispatchWriteTargets(args []*syntax.Word, depth int) []string {
	if depth > maxWrapperDepth || len(args) == 0 {
		return nil
	}
	name, ok := resolveWord(args[0])
	if !ok || name == "" {
		return nil
	}
	name = commandBase(name)

	switch {
	case name == "git":
		return gitWriteTargets(args[1:])
	case name == "dd":
		return ddWriteTargets(args[1:])
	case name == "sed" || name == "perl":
		return inPlaceEditorTargets(args[1:])
	case name == "find":
		return findWriteTargets(args[1:])
	case transparentWrappers[name]:
		eff := wrapperEffectiveArgs(name, args[1:])
		return dispatchWriteTargets(eff, depth+1)
	case contentMutators[name]:
		return operandWords(args[1:], false)
	}
	return nil
}

// wrapperEffectiveArgs skips a wrapper's own flags / env assignments /
// timeout-duration and returns the slice beginning at the wrapped command, or
// nil if it can't be resolved statically (dynamic word → fail to nil, which
// the parser-level Expansion risk flag covers separately).
func wrapperEffectiveArgs(wrapper string, args []*syntax.Word) []*syntax.Word {
	// timeout's first non-flag operand is a DURATION, not the command.
	durationPending := wrapper == "timeout"
	for i := range args {
		w, ok := resolveWord(args[i])
		if !ok {
			return nil
		}
		switch {
		case strings.HasPrefix(w, "-"):
			// A flag; accept that rare separate-word flag arguments aren't
			// modelled (false negatives there are obscure combos).
			continue
		case wrapper == "env" && isEnvAssignment(w):
			continue
		case durationPending:
			durationPending = false
			continue
		default:
			return args[i:]
		}
	}
	return nil
}

// isEnvAssignment reports whether a word is a NAME=VALUE env assignment (the
// leading operands `env` consumes before the command).
func isEnvAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i, r := range w[:eq] {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// findWriteTargets returns the roots and name/path patterns of a
// `find … -delete` or `find … -exec <mutator>` command — the most direct
// mass-deletion vector for test files. Returns nil when find only reads.
func findWriteTargets(args []*syntax.Word) []string {
	var roots, patterns []string
	deletes := false
	collectingRoots := true
	for i := range args {
		w, ok := resolveWord(args[i])
		if !ok {
			collectingRoots = false
			continue
		}
		switch {
		case collectingRoots && !strings.HasPrefix(w, "-"):
			roots = append(roots, w)
			continue
		case w == "-delete":
			deletes = true
		case w == "-exec" || w == "-execdir" || w == "-ok" || w == "-okdir":
			if i+1 < len(args) {
				if c, ok := resolveWord(args[i+1]); ok && contentMutators[commandBase(c)] {
					deletes = true
				}
			}
		case findPatternPredicates[w]:
			if i+1 < len(args) {
				if p, ok := resolveWord(args[i+1]); ok && p != "" {
					patterns = append(patterns, p)
				}
			}
		}
		collectingRoots = false
	}
	if !deletes {
		return nil
	}
	return append(roots, patterns...)
}

// operandWords collects statically-resolvable non-flag words. When
// skipFirstNonFlag is set, the first non-flag word is dropped — used for
// stream editors whose leading operand is the script, not a file.
func operandWords(args []*syntax.Word, skipFirstNonFlag bool) []string {
	var out []string
	skipped := false
	for _, a := range args {
		w, ok := resolveWord(a)
		if !ok || w == "" || strings.HasPrefix(w, "-") {
			continue
		}
		if skipFirstNonFlag && !skipped {
			skipped = true
			continue
		}
		out = append(out, w)
	}
	return out
}

// inPlaceEditorTargets returns the file operands of `sed`/`perl` only when an
// in-place flag (-i, -i.bak, …) is present; otherwise the editor reads and
// writes nothing. The leading non-flag word (the script) is skipped.
func inPlaceEditorTargets(args []*syntax.Word) []string {
	inPlace := false
	for _, a := range args {
		if w, ok := resolveWord(a); ok && strings.HasPrefix(w, "-i") {
			inPlace = true
			break
		}
	}
	if !inPlace {
		return nil
	}
	return operandWords(args, true)
}

// ddWriteTargets extracts dd's of=PATH output operand. The if=PATH input is a
// read and is ignored.
func ddWriteTargets(args []*syntax.Word) []string {
	var out []string
	for _, a := range args {
		w, ok := resolveWord(a)
		if !ok {
			continue
		}
		if of, found := strings.CutPrefix(w, "of="); found && of != "" {
			out = append(out, of)
		}
	}
	return out
}

// gitWriteTargets returns the working-tree paths a mutating git subcommand
// (rm/mv/checkout/restore) would remove, move, or overwrite. args is the slice
// AFTER the `git` word. Git's pre-subcommand global options are skipped —
// including the arg-taking ones (`-C dir`, `--git-dir x`) whose argument would
// otherwise be misread as the subcommand, letting `git -C . rm foo_test.go`
// through.
func gitWriteTargets(args []*syntax.Word) []string {
	i := 0
	for i < len(args) {
		w, ok := resolveWord(args[i])
		if !ok {
			return nil // dynamic global flag → can't locate the subcommand
		}
		if !strings.HasPrefix(w, "-") {
			break // first bare word is the subcommand
		}
		// `--git-dir=foo` is self-contained; `--git-dir foo` / `-C foo`
		// consume the next token; anything else is a standalone flag.
		if eq := strings.IndexByte(w, '='); eq >= 0 {
			i++
			continue
		}
		if gitArgTakingGlobalFlags[w] {
			i += 2
			continue
		}
		i++
	}
	if i >= len(args) {
		return nil
	}
	sub, ok := resolveWord(args[i])
	if !ok || !gitMutatingSubcommands[sub] {
		return nil
	}
	return operandWords(args[i+1:], false)
}

// InterpreterInlineCode returns the inline-code payloads passed to a language
// interpreter via -c / -e / --command / --eval (`python -c "…"`, `node -e "…"`,
// `sh -c "…"`, `perl -e "…"`). Static write analysis cannot see file paths
// inside these strings, so a caller enforcing an integrity boundary (e.g. the
// unattended test-edit guard) should scan the returned payloads for protected
// paths and fail closed. Wrappers (`env python -c …`) are unwrapped.
//
// Returns [ErrUnparseable] (wrapping the parse error) on a command that can't
// be parsed, matching [WriteTargets].
func InterpreterInlineCode(command string) ([]string, error) {
	parser := syntax.NewParser()
	f, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnparseable, err)
	}
	var payloads []string
	syntax.Walk(f, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		payloads = append(payloads, interpreterPayloads(call.Args, 0)...)
		return true
	})
	return payloads, nil
}

// interpreters are language runtimes whose -c/-e flag executes arbitrary code
// (including file writes) that static analysis can't inspect.
var interpreters = map[string]bool{
	"python": true, "python2": true, "python3": true,
	"perl": true, "ruby": true, "node": true, "nodejs": true,
	"php": true, "deno": true, "bun": true,
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
}

// inlineCodeFlags are the per-interpreter flags whose following operand is the
// code string to execute.
var inlineCodeFlags = map[string]bool{
	"-c": true, "-e": true, "--command": true, "--eval": true,
}

func interpreterPayloads(args []*syntax.Word, depth int) []string {
	if depth > maxWrapperDepth || len(args) == 0 {
		return nil
	}
	name, ok := resolveWord(args[0])
	if !ok {
		return nil
	}
	name = commandBase(name)
	if transparentWrappers[name] {
		return interpreterPayloads(wrapperEffectiveArgs(name, args[1:]), depth+1)
	}
	if !interpreters[name] {
		return nil
	}
	var out []string
	for i := 1; i < len(args); i++ {
		w, ok := resolveWord(args[i])
		if !ok {
			continue
		}
		if inlineCodeFlags[w] && i+1 < len(args) {
			if code, ok := resolveWord(args[i+1]); ok && code != "" {
				out = append(out, code)
			}
		}
	}
	return out
}

// commandBase strips a leading path from a command name so `/usr/bin/rm` is
// matched like `rm`.
func commandBase(cmd string) string {
	if i := strings.LastIndex(cmd, "/"); i >= 0 {
		return cmd[i+1:]
	}
	return cmd
}
