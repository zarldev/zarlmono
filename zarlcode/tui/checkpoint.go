package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/filesystem"
)

var ErrCheckpointNotFound = errors.New("checkpoint not found")
var ErrCheckpointConflict = errors.New("checkpoint conflict")

type FileImage struct {
	Content []byte
	Missing bool
}

type FileCheckpoint struct {
	Path      string
	Before    FileImage
	After     FileImage
	Mutations int
	Additions int
	Deletions int
}

type TurnCheckpoint struct {
	TurnID      string
	TurnOrdinal int
	StartedAt   time.Time
	CompletedAt time.Time
	Files       map[string]FileCheckpoint
}

type Checkpoints struct {
	workspaceDir string
	active       *TurnCheckpoint
	turns        []TurnCheckpoint
	byTurn       map[string]int
}

type RollbackPlan struct {
	TurnID   string
	Path     string
	Files    []RollbackFilePlan
	Conflict bool
}

type RollbackFilePlan struct {
	Path     string
	Action   string
	Conflict bool
}

func NewCheckpoints(workspaceDir string) *Checkpoints {
	return &Checkpoints{workspaceDir: filepath.Clean(workspaceDir), byTurn: make(map[string]int)}
}

func (c *Checkpoints) SetWorkspaceDir(workspaceDir string) {
	if c == nil {
		return
	}
	c.workspaceDir = filepath.Clean(workspaceDir)
}

func (c *Checkpoints) StartTurn(turnID string, turnOrdinal int, at time.Time) {
	if c == nil || turnID == "" {
		return
	}
	c.active = &TurnCheckpoint{
		TurnID:      turnID,
		TurnOrdinal: turnOrdinal,
		StartedAt:   at,
		Files:       make(map[string]FileCheckpoint),
	}
}

func (c *Checkpoints) CompleteTurn(turnID string, at time.Time) {
	if c == nil || c.active == nil || c.active.TurnID != turnID {
		return
	}
	cp := cloneTurnCheckpoint(*c.active)
	cp.CompletedAt = at
	c.byTurn[cp.TurnID] = len(c.turns)
	c.turns = append(c.turns, cp)
	c.active = nil
}

func (c *Checkpoints) RecordMutation(m WorkingSetMutation, before []byte, beforeMissing bool, after []byte, afterMissing bool) {
	if c == nil || c.active == nil || m.TurnID == "" || c.active.TurnID != m.TurnID {
		return
	}
	path := filepath.ToSlash(filepath.Clean(m.Path))
	if path == "." || path == "" {
		return
	}
	fc, ok := c.active.Files[path]
	if !ok {
		fc = FileCheckpoint{
			Path: path,
			Before: FileImage{
				Content: append([]byte(nil), before...),
				Missing: beforeMissing,
			},
		}
	}
	fc.After = FileImage{Content: append([]byte(nil), after...), Missing: afterMissing}
	fc.Mutations++
	fc.Additions += m.Additions
	fc.Deletions += m.Deletions
	c.active.Files[path] = fc
}

func (c *Checkpoints) CheckpointForTurn(turnID string) (TurnCheckpoint, bool) {
	if c == nil || turnID == "" {
		return TurnCheckpoint{}, false
	}
	if c.active != nil && c.active.TurnID == turnID {
		return cloneTurnCheckpoint(*c.active), true
	}
	idx, ok := c.byTurn[turnID]
	if !ok || idx < 0 || idx >= len(c.turns) {
		return TurnCheckpoint{}, false
	}
	return cloneTurnCheckpoint(c.turns[idx]), true
}

func (c *Checkpoints) Turns() []TurnCheckpoint {
	if c == nil || len(c.turns) == 0 {
		return nil
	}
	turns := make([]TurnCheckpoint, 0, len(c.turns))
	for _, cp := range c.turns {
		turns = append(turns, cloneTurnCheckpoint(cp))
	}
	return turns
}

func (c *Checkpoints) RestoreTurn(turnID string) error {
	plan, err := c.PlanRestoreTurn(turnID)
	if err != nil {
		return err
	}
	if plan.Conflict {
		return fmt.Errorf("%w: turn %q", ErrCheckpointConflict, turnID)
	}
	cp, _ := c.CheckpointForTurn(turnID)
	for _, file := range plan.Files {
		if err := c.restoreImage(file.Path, cp.Files[file.Path].Before); err != nil {
			return fmt.Errorf("restore %s: %w", file.Path, err)
		}
	}
	return nil
}

func (c *Checkpoints) RestoreFile(turnID, path string) error {
	plan, err := c.PlanRestoreFile(turnID, path)
	if err != nil {
		return err
	}
	if plan.Conflict {
		return fmt.Errorf("%w: turn %q file %q", ErrCheckpointConflict, turnID, path)
	}
	cp, _ := c.CheckpointForTurn(turnID)
	fc := cp.Files[plan.Files[0].Path]
	if err := c.restoreImage(fc.Path, fc.Before); err != nil {
		return fmt.Errorf("restore %s: %w", fc.Path, err)
	}
	return nil
}

func (c *Checkpoints) PlanRestoreTurn(turnID string) (RollbackPlan, error) {
	cp, ok := c.CheckpointForTurn(turnID)
	if !ok {
		return RollbackPlan{}, fmt.Errorf("%w: turn %q", ErrCheckpointNotFound, turnID)
	}
	files := make([]string, 0, len(cp.Files))
	for path := range cp.Files {
		files = append(files, path)
	}
	slices.Sort(files)
	plan := RollbackPlan{TurnID: turnID}
	for _, path := range files {
		fp, err := c.planFile(cp.Files[path])
		if err != nil {
			return RollbackPlan{}, fmt.Errorf("plan %s: %w", path, err)
		}
		if fp.Conflict {
			plan.Conflict = true
		}
		plan.Files = append(plan.Files, fp)
	}
	return plan, nil
}

func (c *Checkpoints) PlanRestoreFile(turnID, path string) (RollbackPlan, error) {
	cp, ok := c.CheckpointForTurn(turnID)
	if !ok {
		return RollbackPlan{}, fmt.Errorf("%w: turn %q", ErrCheckpointNotFound, turnID)
	}
	path = filepath.ToSlash(filepath.Clean(path))
	fc, ok := cp.Files[path]
	if !ok {
		return RollbackPlan{}, fmt.Errorf("%w: turn %q file %q", ErrCheckpointNotFound, turnID, path)
	}
	fp, err := c.planFile(fc)
	if err != nil {
		return RollbackPlan{}, fmt.Errorf("plan %s: %w", path, err)
	}
	return RollbackPlan{TurnID: turnID, Path: path, Files: []RollbackFilePlan{fp}, Conflict: fp.Conflict}, nil
}

func (c *Checkpoints) planFile(fc FileCheckpoint) (RollbackFilePlan, error) {
	current, err := c.currentImage(fc.Path)
	if err != nil {
		return RollbackFilePlan{}, err
	}
	fp := RollbackFilePlan{Path: fc.Path, Action: rollbackAction(fc.Before)}
	if current.Missing != fc.After.Missing || !bytes.Equal(current.Content, fc.After.Content) {
		fp.Conflict = true
	}
	return fp, nil
}

func (c *Checkpoints) currentImage(path string) (FileImage, error) {
	abs, err := resolveCheckpointPath(c.workspaceDir, path)
	if err != nil {
		return FileImage{}, err
	}
	data, err := os.ReadFile(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return FileImage{Missing: true}, nil
	case err != nil:
		return FileImage{}, fmt.Errorf("read current file: %w", err)
	default:
		return FileImage{Content: data}, nil
	}
}

func rollbackAction(before FileImage) string {
	if before.Missing {
		return "delete"
	}
	return "restore"
}

func (c *Checkpoints) restoreImage(path string, image FileImage) error {
	abs, err := resolveCheckpointPath(c.workspaceDir, path)
	if err != nil {
		return err
	}
	if image.Missing {
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove file: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), filesystem.ModePublicDir); err != nil {
		return fmt.Errorf("create parent directories: %w", err)
	}
	if err := os.WriteFile(abs, image.Content, filesystem.ModePublicFile); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func resolveCheckpointPath(root, path string) (string, error) {
	if root == "" || root == "." {
		return "", errors.New("workspace root unavailable")
	}
	cleanRoot := filepath.Clean(root)
	var clean string
	if filepath.IsAbs(path) {
		clean = filepath.Clean(path)
	} else {
		clean = filepath.Join(cleanRoot, path)
	}
	sep := string(filepath.Separator)
	if clean != cleanRoot && !strings.HasPrefix(clean+sep, cleanRoot+sep) {
		return "", fmt.Errorf("path %q escapes workspace", path)
	}
	return clean, nil
}

func cloneTurnCheckpoint(cp TurnCheckpoint) TurnCheckpoint {
	out := cp
	out.Files = make(map[string]FileCheckpoint, len(cp.Files))
	for path, fc := range cp.Files {
		out.Files[path] = cloneFileCheckpoint(fc)
	}
	return out
}

func cloneFileCheckpoint(fc FileCheckpoint) FileCheckpoint {
	fc.Before.Content = append([]byte(nil), fc.Before.Content...)
	fc.After.Content = append([]byte(nil), fc.After.Content...)
	return fc
}
