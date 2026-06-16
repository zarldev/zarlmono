package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

type PersonID string

// Person stores one or more 128-dim face embeddings per identity. Multi-pose
// enrolment (front / left / right) is captured during onboarding; matching
// uses the closest of all stored embeddings. Legacy single-embedding rows
// transparently load as a one-element Embeddings slice.
type Person struct {
	ID         PersonID
	Name       string
	Embeddings [][]float32
	Notes      string
	Photo      string
}

const EuclideanMatchThreshold = 0.6

type PersonRepo struct {
	q *gen.Queries
}

func NewPersonRepo(q *gen.Queries) *PersonRepo {
	return &PersonRepo{q: q}
}

// Match returns the person whose closest pose embedding is nearest the probe,
// provided that distance is below threshold.
func (r *PersonRepo) Match(ctx context.Context, probe []float32, threshold float64) (Person, float64, error) {
	rows, err := r.q.ListPersons(ctx)
	if err != nil {
		return Person{}, 0, fmt.Errorf("list persons: %w", err)
	}

	var bestPerson Person
	bestDist := math.MaxFloat64

	for _, row := range rows {
		embs, err := parseEmbeddings(row.Embedding)
		if err != nil {
			continue
		}
		for _, stored := range embs {
			dist := euclideanDistance(probe, stored)
			if dist < bestDist {
				bestDist = dist
				bestPerson = Person{
					ID:         PersonID(row.ID),
					Name:       row.Name,
					Embeddings: embs,
					Notes:      row.Notes,
					Photo:      row.Photo,
				}
			}
		}
	}

	if bestDist > threshold {
		return Person{}, bestDist, nil
	}
	return bestPerson, bestDist, nil
}

// Create persists a new person. Pass one embedding for legacy single-pose
// enrolment, three for the wizard flow.
func (r *PersonRepo) Create(ctx context.Context, name string, embeddings [][]float32, photo string) (Person, error) {
	if len(embeddings) == 0 {
		return Person{}, errors.New("create person: at least one embedding required")
	}
	id := uuid.New().String()
	embJSON, err := json.Marshal(embeddings)
	if err != nil {
		return Person{}, fmt.Errorf("marshal embeddings: %w", err)
	}
	if err := r.q.CreatePerson(ctx, gen.CreatePersonParams{
		ID:        id,
		Name:      name,
		Embedding: embJSON,
		Notes:     "",
		Photo:     photo,
	}); err != nil {
		return Person{}, fmt.Errorf("create person: %w", err)
	}
	return Person{ID: PersonID(id), Name: name, Embeddings: embeddings, Photo: photo}, nil
}

func (r *PersonRepo) DeleteByName(ctx context.Context, name string) error {
	if err := r.q.DeletePersonByName(ctx, name); err != nil {
		return fmt.Errorf("delete person: %w", err)
	}
	return nil
}

func (r *PersonRepo) GetByName(ctx context.Context, name string) (Person, error) {
	row, err := r.q.GetPersonByName(ctx, name)
	if err != nil {
		return Person{}, fmt.Errorf("get person: %w", err)
	}
	embs, _ := parseEmbeddings(row.Embedding)
	return Person{
		ID:         PersonID(row.ID),
		Name:       row.Name,
		Embeddings: embs,
		Notes:      row.Notes,
		Photo:      row.Photo,
	}, nil
}

func (r *PersonRepo) List(ctx context.Context) ([]Person, error) {
	rows, err := r.q.ListPersons(ctx)
	if err != nil {
		return nil, fmt.Errorf("list persons: %w", err)
	}
	persons := make([]Person, 0, len(rows))
	for _, row := range rows {
		embs, _ := parseEmbeddings(row.Embedding)
		persons = append(persons, Person{
			ID:         PersonID(row.ID),
			Name:       row.Name,
			Embeddings: embs,
			Notes:      row.Notes,
			Photo:      row.Photo,
		})
	}
	return persons, nil
}

func (r *PersonRepo) UpdateNotes(ctx context.Context, id PersonID, name string, notes string) error {
	if err := r.q.UpdatePersonNotes(ctx, gen.UpdatePersonNotesParams{
		Name:  name,
		Notes: notes,
		ID:    string(id),
	}); err != nil {
		return fmt.Errorf("update notes: %w", err)
	}
	return nil
}

func (r *PersonRepo) Delete(ctx context.Context, id PersonID) error {
	return r.q.DeletePerson(ctx, string(id))
}

// DeleteAll wipes every person row. Used by the agent reset flow.
func (r *PersonRepo) DeleteAll(ctx context.Context) (int64, error) {
	n, err := r.q.DeleteAllPersons(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete all persons: %w", err)
	}
	return n, nil
}

// parseEmbeddings decodes the JSON column. Tries the multi-pose
// list-of-arrays shape first; falls back to a flat 128-float array
// (legacy single-pose enrolment) and promotes it to a one-element list.
func parseEmbeddings(raw json.RawMessage) ([][]float32, error) {
	var multi [][]float32
	if err := json.Unmarshal(raw, &multi); err == nil && len(multi) > 0 && len(multi[0]) == 128 {
		return multi, nil
	}
	var single []float32
	if err := json.Unmarshal(raw, &single); err == nil && len(single) == 128 {
		return [][]float32{single}, nil
	}
	return nil, errors.New("embedding JSON not parseable as 128-float array or list of 128-float arrays")
}

func euclideanDistance(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return math.MaxFloat64
	}
	var sum float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return math.Sqrt(sum)
}
