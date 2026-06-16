package tui

import (
	"cmp"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// WorkingSet records file mutations observed during this TUI session. It is a
// local, in-memory read model for future panes; persistence and rendering stay
// outside this type.
type WorkingSet struct {
	workspaceDir string

	activeTurnID      string
	activeTurnOrdinal int
	nextTurnOrdinal   int
	nextMutationID    int

	files         map[string]workingSetFileState
	mutations     []WorkingSetMutation
	fileMutations map[string][]int
	turnMutations map[string][]int
}

type workingSetFileState struct {
	path           string
	firstChangedAt time.Time
	lastChangedAt  time.Time
	additions      int
	deletions      int
	mutations      int
	firstOrdinal   int
}

// WorkingSetFile is the coalesced session or turn summary for one changed file.
type WorkingSetFile struct {
	Path           string
	FirstChangedAt time.Time
	LastChangedAt  time.Time
	Additions      int
	Deletions      int
	Mutations      int
}

// WorkingSetTurn is the coalesced summary for files changed during one turn.
type WorkingSetTurn struct {
	ID             string
	Ordinal        int
	FirstChangedAt time.Time
	LastChangedAt  time.Time
	Files          int
	Mutations      int
	Additions      int
	Deletions      int
}

// WorkingSetMutation is one recorded file mutation and the turn it belongs to.
type WorkingSetMutation struct {
	Path            string
	Diff            string
	TurnID          string
	TurnOrdinal     int
	ChangedAt       time.Time
	MutationOrdinal int
	Additions       int
	Deletions       int
}

// NewWorkingSet returns an empty working-set model for workspaceDir.
func NewWorkingSet(workspaceDir string) *WorkingSet {
	return &WorkingSet{
		workspaceDir:    filepath.Clean(workspaceDir),
		files:           make(map[string]workingSetFileState),
		fileMutations:   make(map[string][]int),
		turnMutations:   make(map[string][]int),
		nextTurnOrdinal: 1,
		nextMutationID:  1,
	}
}

// SetWorkspaceDir updates the root used to normalize absolute paths into
// workspace-relative paths. Existing mutations keep the path recorded at the
// time they were observed.
func (w *WorkingSet) SetWorkspaceDir(workspaceDir string) {
	if w == nil {
		return
	}
	w.workspaceDir = filepath.Clean(workspaceDir)
}

// StartTurn marks a top-level runner turn as the active attribution target for
// subsequent diffs.
func (w *WorkingSet) StartTurn(turnID string) int {
	if w == nil {
		return 0
	}
	w.activeTurnID = turnID
	w.activeTurnOrdinal = w.nextTurnOrdinal
	w.nextTurnOrdinal++
	return w.activeTurnOrdinal
}

// CompleteTurn clears active attribution once the top-level runner turn ends.
func (w *WorkingSet) CompleteTurn(turnID string) {
	if w == nil || turnID != w.activeTurnID {
		return
	}
	w.activeTurnID = ""
	w.activeTurnOrdinal = 0
}

// RecordDiff stores one file mutation using the current time.
func (w *WorkingSet) RecordDiff(path, diff string) WorkingSetMutation {
	return w.recordDiffAt(path, diff, time.Now())
}

func (w *WorkingSet) recordDiffAt(path, diff string, at time.Time) WorkingSetMutation {
	if w == nil {
		return WorkingSetMutation{}
	}
	path = w.relativePath(path)
	add, del := diffLineCounts(diff)
	mutation := WorkingSetMutation{
		Path:            path,
		Diff:            diff,
		TurnID:          w.activeTurnID,
		TurnOrdinal:     w.activeTurnOrdinal,
		ChangedAt:       at,
		MutationOrdinal: w.nextMutationID,
		Additions:       add,
		Deletions:       del,
	}
	w.nextMutationID++

	idx := len(w.mutations)
	w.mutations = append(w.mutations, mutation)
	w.fileMutations[path] = append(w.fileMutations[path], idx)
	if mutation.TurnID != "" {
		w.turnMutations[mutation.TurnID] = append(w.turnMutations[mutation.TurnID], idx)
	}

	state, ok := w.files[path]
	if !ok {
		state = workingSetFileState{
			path:           path,
			firstChangedAt: at,
			firstOrdinal:   mutation.MutationOrdinal,
		}
	}
	state.lastChangedAt = at
	state.additions += add
	state.deletions += del
	state.mutations++
	w.files[path] = state
	return mutation
}

// FilesChangedThisSession returns one coalesced summary per changed file,
// ordered by first mutation in the session.
func (w *WorkingSet) FilesChangedThisSession() []WorkingSetFile {
	if w == nil || len(w.files) == 0 {
		return nil
	}
	states := slices.SortedFunc(maps.Values(w.files), func(a, b workingSetFileState) int {
		return cmp.Compare(a.firstOrdinal, b.firstOrdinal)
	})
	files := make([]WorkingSetFile, 0, len(states))
	for i := range states {
		files = append(files, fileSummaryFromState(&states[i]))
	}
	return files
}

// MutationsThisSession returns the full per-mutation history for the session.
func (w *WorkingSet) MutationsThisSession() []WorkingSetMutation {
	if w == nil || len(w.mutations) == 0 {
		return nil
	}
	mutations := make([]WorkingSetMutation, len(w.mutations))
	copy(mutations, w.mutations)
	return mutations
}

// DiffBodies returns the latest unified-diff body per changed file. This
// is the persistable shape stored in a session's diff_bodies_json so the
// Files dock and diff viewer repopulate on -continue. Nil when nothing
// changed.
func (w *WorkingSet) DiffBodies() map[string]string {
	if w == nil || len(w.files) == 0 {
		return nil
	}
	out := make(map[string]string, len(w.files))
	for path, idxs := range w.fileMutations {
		if len(idxs) == 0 {
			continue
		}
		out[path] = w.mutations[idxs[len(idxs)-1]].Diff // latest body wins
	}
	return out
}

// RestoreDiffBodies replays persisted diff bodies into an empty working
// set so the Files dock and diff viewer reflect the resumed session.
// Each path becomes a single mutation outside any turn; counts are
// recomputed from the body. Paths are replayed in sorted order so the
// dock ordering is deterministic across restores.
func (w *WorkingSet) RestoreDiffBodies(bodies map[string]string, at time.Time) {
	if w == nil || len(bodies) == 0 {
		return
	}
	for _, path := range slices.Sorted(maps.Keys(bodies)) {
		w.recordDiffAt(path, bodies[path], at)
	}
}

// FilesChangedForTurn returns one coalesced summary per file changed during the
// given turn, ordered by first mutation within that turn.
func (w *WorkingSet) FilesChangedForTurn(turnID string) []WorkingSetFile {
	if w == nil || turnID == "" {
		return nil
	}
	return w.aggregateFiles(w.turnMutations[turnID])
}

// TurnsChangedThisSession returns one summary per turn that changed files,
// ordered by the first mutation observed in each turn. Diffs observed outside a
// top-level turn are intentionally excluded because they have no turn identity.
func (w *WorkingSet) TurnsChangedThisSession() []WorkingSetTurn {
	if w == nil || len(w.mutations) == 0 {
		return nil
	}
	type aggregate struct {
		turn         WorkingSetTurn
		firstOrdinal int
		files        map[string]struct{}
	}
	byTurn := make(map[string]aggregate)
	for _, mutation := range w.mutations {
		if mutation.TurnID == "" {
			continue
		}
		agg, ok := byTurn[mutation.TurnID]
		if !ok {
			agg = aggregate{
				turn: WorkingSetTurn{
					ID:             mutation.TurnID,
					Ordinal:        mutation.TurnOrdinal,
					FirstChangedAt: mutation.ChangedAt,
				},
				firstOrdinal: mutation.MutationOrdinal,
				files:        make(map[string]struct{}),
			}
			byTurn[mutation.TurnID] = agg
		}
		agg.turn.LastChangedAt = mutation.ChangedAt
		agg.turn.Mutations++
		agg.turn.Additions += mutation.Additions
		agg.turn.Deletions += mutation.Deletions
		agg.files[mutation.Path] = struct{}{}
		byTurn[mutation.TurnID] = agg
	}

	aggs := slices.SortedFunc(maps.Values(byTurn), func(a, b aggregate) int {
		return cmp.Compare(a.firstOrdinal, b.firstOrdinal)
	})
	turns := make([]WorkingSetTurn, 0, len(aggs))
	for _, agg := range aggs {
		turn := agg.turn
		turn.Files = len(agg.files) // distinct paths changed this turn
		turns = append(turns, turn)
	}
	return turns
}

// MutationsForFile returns the full per-mutation history for path.
func (w *WorkingSet) MutationsForFile(path string) []WorkingSetMutation {
	if w == nil {
		return nil
	}
	return w.copyMutations(w.fileMutations[w.relativePath(path)])
}

// MutationsForTurn returns the full per-mutation history for turnID.
func (w *WorkingSet) MutationsForTurn(turnID string) []WorkingSetMutation {
	if w == nil || turnID == "" {
		return nil
	}
	return w.copyMutations(w.turnMutations[turnID])
}

func (w *WorkingSet) aggregateFiles(indices []int) []WorkingSetFile {
	if len(indices) == 0 {
		return nil
	}
	type aggregate struct {
		file         WorkingSetFile
		firstOrdinal int
	}
	byPath := make(map[string]aggregate)
	for _, idx := range indices {
		mutation := w.mutations[idx]
		agg, ok := byPath[mutation.Path]
		if !ok {
			agg = aggregate{
				file: WorkingSetFile{
					Path:           mutation.Path,
					FirstChangedAt: mutation.ChangedAt,
				},
				firstOrdinal: mutation.MutationOrdinal,
			}
			byPath[mutation.Path] = agg
		}
		agg.file.LastChangedAt = mutation.ChangedAt
		agg.file.Additions += mutation.Additions
		agg.file.Deletions += mutation.Deletions
		agg.file.Mutations++
		byPath[mutation.Path] = agg
	}

	aggs := make([]aggregate, 0, len(byPath))
	for _, agg := range byPath {
		aggs = append(aggs, agg)
	}
	slices.SortFunc(aggs, func(a, b aggregate) int { return cmp.Compare(a.firstOrdinal, b.firstOrdinal) })
	files := make([]WorkingSetFile, 0, len(aggs))
	for _, agg := range aggs {
		files = append(files, agg.file)
	}
	return files
}

func (w *WorkingSet) copyMutations(indices []int) []WorkingSetMutation {
	if len(indices) == 0 {
		return nil
	}
	mutations := make([]WorkingSetMutation, 0, len(indices))
	for _, idx := range indices {
		mutations = append(mutations, w.mutations[idx])
	}
	return mutations
}

func fileSummaryFromState(state *workingSetFileState) WorkingSetFile {
	return WorkingSetFile{
		Path:           state.path,
		FirstChangedAt: state.firstChangedAt,
		LastChangedAt:  state.lastChangedAt,
		Additions:      state.additions,
		Deletions:      state.deletions,
		Mutations:      state.mutations,
	}
}

func (w *WorkingSet) relativePath(path string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) && w.workspaceDir != "" && w.workspaceDir != "." {
		if rel, err := filepath.Rel(w.workspaceDir, clean); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			clean = rel
		}
	}
	if clean == "." {
		return ""
	}
	return filepath.ToSlash(clean)
}
