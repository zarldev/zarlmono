package shellpolicy

import (
	"fmt"
	"strings"
)

// verifyDeniedCommands are the command keys (post tier-2 normalisation, so
// git/go carry their subcommand) blocked under the verify profile in
// addition to the standard rules. Two groups:
//
//   - state mutators WriteTargets can't model as file operands: directory /
//     metadata ops, repo-state git subcommands, module-mutating go
//     subcommands.
//   - content mutators WriteTargets DOES model — kept here as belt and
//     braces for the forms static operand resolution skips (dynamic words:
//     `rm $F` yields no targets but the head still says rm).
//
// Deliberately NOT here: package managers (npm/pip install — tests often
// need deps materialised), kill/pkill (a verify run may stop its own
// background test server), and plain sed/perl (pure stream filters without
// -i; the -i forms surface through WriteTargets).
var verifyDeniedCommands = map[string]bool{
	// filesystem / metadata
	"touch": true, "mkdir": true, "rmdir": true, "chmod": true,
	"chown": true, "chgrp": true, "patch": true, "shred": true,
	"rsync": true, "mkfifo": true,
	// content mutators (also covered by WriteTargets when operands resolve)
	"rm": true, "unlink": true, "mv": true, "cp": true, "tee": true,
	"truncate": true, "ln": true, "install": true, "dd": true,
	// wrapper executors: the wrapped command head is an argument word the
	// IR can't see (`xargs rm` records only xargs), so the tunnel is
	// closed wholesale; run the command directly instead.
	"xargs": true, "parallel": true,
	// repo-state git (worktree-writing subcommands also reach WriteTargets)
	"git commit": true, "git checkout": true, "git switch": true,
	"git restore": true, "git reset": true, "git clean": true,
	"git stash": true, "git merge": true, "git rebase": true,
	"git cherry-pick": true, "git revert": true, "git am": true,
	"git apply": true, "git rm": true, "git mv": true,
	"git push": true, "git pull": true,
	// module-mutating go
	"go mod": true, "go get": true, "go generate": true,
}

// DecideVerify converts an IR into a Decision under the verify profile:
// the standard rules first (version / syntax / cd / redirect), then the
// verify-mode additions — a verify sub-agent runs tests, builds, and
// linters and reports what it finds; it does not modify the workspace.
//
// writeTargets is the output of [WriteTargets] for the same command: the
// statically-resolvable file operands of content mutators (rm, mv, cp,
// tee, sed -i, dd of=, git checkout/restore/rm/mv, find -delete/-exec,
// redirects, wrapper-tunnelled forms). Any non-empty set blocks. The
// command-key deny-list then catches mutators whose operands didn't
// resolve statically and state mutations that have no file operand at all
// (git commit, go mod tidy, mkdir).
//
// This is a hardened boundary, not a sandbox: static analysis cannot see
// through `eval`, interpreter one-liners (`python -c "..."`), or scripts
// invoked by path. It raises the bar from "any bash mutates" to "mutation
// requires deliberate evasion" — consistent with the code package's own
// "powerful and intentionally not a sandbox" stance.
func (e PolicyEngine) DecideVerify(ir ParsedIR, writeTargets []string) Decision {
	d := e.Decide(ir)
	if d.IsBlocked {
		return d
	}

	if len(writeTargets) > 0 {
		d.IsBlocked = true
		d.BlockReason = fmt.Sprintf(
			"shell policy (verify mode): this command writes to %s — a verify sub-agent runs tests and reports findings; it does not modify the workspace. "+
				"Report the needed change back to the parent agent, which can apply it in implement mode",
			strings.Join(writeTargets, ", "),
		)
		return d
	}

	for _, cmd := range ir.Commands {
		if verifyDeniedCommands[cmd] {
			d.IsBlocked = true
			d.BlockReason = fmt.Sprintf(
				"shell policy (verify mode): %q mutates workspace or repository state, which a verify sub-agent must not do. "+
					"Run your tests/builds read-only and report the needed change back to the parent agent, which can apply it in implement mode",
				cmd,
			)
			return d
		}
	}

	return d
}
