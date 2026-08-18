package shellpolicy

// unscopedMutationCommands mutate files or repository state without exposing
// reliable destination operands to WriteTargets. Consumers that protect a
// subset of the tree must reject these commands wholesale rather than assume
// an empty target list means read-only.
var unscopedMutationCommands = map[string]bool{
	"patch":           true,
	"git reset":       true,
	"git clean":       true,
	"git stash":       true,
	"git merge":       true,
	"git rebase":      true,
	"git cherry-pick": true,
	"git revert":      true,
	"git am":          true,
	"git apply":       true,
}

// UnscopedMutationCommand returns the first command whose writes cannot be
// narrowed to explicit file operands, or "" when every command is either
// read-only or handled by WriteTargets.
func UnscopedMutationCommand(command string) (string, error) {
	ir, err := NewUnixParser().Parse(command)
	if err != nil {
		return "", ErrUnparseable
	}
	for _, name := range ir.Commands {
		if unscopedMutationCommands[name] {
			return name, nil
		}
	}
	return "", nil
}
