package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

const timeFormat = "2006-01-02 15:04:05"

type TaskID string

type Task struct {
	ID            string
	Prompt        string
	Status        string
	Summary       string
	Iterations    int
	MaxIterations int
	PersonName    string
	SessionID     string
	Schedule      string
	ProfileName   string
	WorkspaceName string
	CreatedAt     string
	UpdatedAt     string
}

type TaskRepo struct {
	q *gen.Queries
}

func NewTaskRepo(q *gen.Queries) *TaskRepo {
	return &TaskRepo{q: q}
}

func (r *TaskRepo) Create(ctx context.Context, prompt, personName, sessionID, schedule, profileName, workspaceName string, maxIterations int) (Task, error) {
	if maxIterations <= 0 {
		maxIterations = 20
	}
	if profileName == "" {
		profileName = "default"
	}
	id := uuid.New().String()
	err := r.q.CreateTask(ctx, gen.CreateTaskParams{
		ID:            id,
		Prompt:        prompt,
		Status:        "pending",
		MaxIterations: int32(maxIterations),
		PersonName:    personName,
		SessionID:     sessionID,
		Schedule:      schedule,
		ProfileName:   profileName,
		WorkspaceName: sql.NullString{String: workspaceName, Valid: workspaceName != ""},
	})
	if err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	row, err := r.q.GetTask(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("get task after create: %w", err)
	}
	return rowToTask(row), nil
}

func (r *TaskRepo) Get(ctx context.Context, id TaskID) (Task, error) {
	row, err := r.q.GetTask(ctx, string(id))
	if err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return rowToTask(row), nil
}

func (r *TaskRepo) List(ctx context.Context, limit, offset int) ([]Task, int, error) {
	rows, err := r.q.ListTasks(ctx, gen.ListTasksParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list tasks: %w", err)
	}
	count, err := r.q.CountTasks(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count tasks: %w", err)
	}
	tasks := make([]Task, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, rowToTask(row))
	}
	return tasks, int(count), nil
}

func (r *TaskRepo) ListPending(ctx context.Context) ([]Task, error) {
	rows, err := r.q.ListPendingTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending tasks: %w", err)
	}
	tasks := make([]Task, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, rowToTask(row))
	}
	return tasks, nil
}

// ResetOrphanRunning flips every task still marked running back to pending.
// Called by the runner at startup so tasks interrupted by a zarl crash are
// picked up again rather than stuck forever in `running`.
func (r *TaskRepo) ResetOrphanRunning(ctx context.Context) (int64, error) {
	n, err := r.q.ResetOrphanRunningTasks(ctx)
	if err != nil {
		return 0, fmt.Errorf("reset orphan running tasks: %w", err)
	}
	return n, nil
}

func (r *TaskRepo) ListScheduled(ctx context.Context) ([]Task, error) {
	rows, err := r.q.ListScheduledTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list scheduled tasks: %w", err)
	}
	tasks := make([]Task, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, rowToTask(row))
	}
	return tasks, nil
}

func (r *TaskRepo) SetStatus(ctx context.Context, id TaskID, status string) error {
	err := r.q.UpdateTaskStatus(ctx, gen.UpdateTaskStatusParams{
		Status: status,
		ID:     string(id),
	})
	if err != nil {
		return fmt.Errorf("set task status: %w", err)
	}
	return nil
}

// SetWorkspace sets the workspace name for an existing task.
func (r *TaskRepo) SetWorkspace(ctx context.Context, id TaskID, workspaceName string) error {
	err := r.q.SetTaskWorkspace(ctx, gen.SetTaskWorkspaceParams{
		ID:            string(id),
		WorkspaceName: sql.NullString{String: workspaceName, Valid: workspaceName != ""},
	})
	if err != nil {
		return fmt.Errorf("set task workspace: %w", err)
	}
	return nil
}

func (r *TaskRepo) UpdateProgress(ctx context.Context, id TaskID, iterations int, summary string) error {
	err := r.q.UpdateTaskProgress(ctx, gen.UpdateTaskProgressParams{
		Iterations: int32(iterations),
		Summary:    summary,
		ID:         string(id),
	})
	if err != nil {
		return fmt.Errorf("update task progress: %w", err)
	}
	return nil
}

func (r *TaskRepo) Complete(ctx context.Context, id TaskID, iterations int, summary string) error {
	err := r.q.UpdateTaskComplete(ctx, gen.UpdateTaskCompleteParams{
		Iterations: int32(iterations),
		Summary:    summary,
		ID:         string(id),
	})
	if err != nil {
		return fmt.Errorf("complete task: %w", err)
	}
	return nil
}

func (r *TaskRepo) CreateTask(ctx context.Context, prompt, personName, sessionID, schedule, profileName, workspaceName string, maxIterations int) (string, error) {
	t, err := r.Create(ctx, prompt, personName, sessionID, schedule, profileName, workspaceName, maxIterations)
	if err != nil {
		return "", err
	}
	return t.ID, nil
}

func (r *TaskRepo) UpdateSchedule(ctx context.Context, taskID, schedule string) error {
	return r.q.UpdateTaskSchedule(ctx, gen.UpdateTaskScheduleParams{
		Schedule: schedule,
		ID:       taskID,
	})
}

func (r *TaskRepo) Delete(ctx context.Context, id TaskID) error {
	err := r.q.DeleteTask(ctx, string(id))
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

func rowToTask(row gen.Task) Task {
	t := Task{
		ID:            row.ID,
		Prompt:        row.Prompt,
		Status:        row.Status,
		Summary:       row.Summary,
		Iterations:    int(row.Iterations),
		MaxIterations: int(row.MaxIterations),
		PersonName:    row.PersonName,
		SessionID:     row.SessionID,
		Schedule:      row.Schedule,
		ProfileName:   row.ProfileName,
		CreatedAt:     row.CreatedAt.Format(timeFormat),
		UpdatedAt:     row.UpdatedAt.Format(timeFormat),
	}
	if row.WorkspaceName.Valid {
		t.WorkspaceName = row.WorkspaceName.String
	}
	return t
}
