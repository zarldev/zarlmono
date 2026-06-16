package repository_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/repository"
)

func TestPersonRepoCreateAndMatch(t *testing.T) {
	q := testDB(t)
	ctx := context.Background()
	repo := repository.NewPersonRepo(q)

	emb := make([]float32, 128)
	emb[0] = 1.0

	p, err := repo.Create(ctx, "TestPerson", [][]float32{emb}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { repo.DeleteByName(ctx, "TestPerson") })

	if p.Name != "TestPerson" {
		t.Errorf("name = %q, want TestPerson", p.Name)
	}

	matched, dist, err := repo.Match(ctx, emb, repository.EuclideanMatchThreshold)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if matched.Name != "TestPerson" {
		t.Errorf("match name = %q, want TestPerson", matched.Name)
	}
	if dist > 0.01 {
		t.Errorf("match distance = %f, want ~0", dist)
	}
}

func TestPersonRepoNoMatch(t *testing.T) {
	q := testDB(t)
	ctx := context.Background()
	repo := repository.NewPersonRepo(q)

	emb1 := make([]float32, 128)
	emb1[0] = 1.0
	_, err := repo.Create(ctx, "TestNoMatch", [][]float32{emb1}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { repo.DeleteByName(ctx, "TestNoMatch") })

	emb2 := make([]float32, 128)
	emb2[1] = 1.0
	matched, _, err := repo.Match(ctx, emb2, repository.EuclideanMatchThreshold)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if matched.Name != "" {
		t.Errorf("expected no match, got %q", matched.Name)
	}
}

func TestPersonRepoDeleteByName(t *testing.T) {
	q := testDB(t)
	ctx := context.Background()
	repo := repository.NewPersonRepo(q)

	emb := make([]float32, 128)
	emb[0] = 1.0
	_, err := repo.Create(ctx, "TestDelete", [][]float32{emb}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.DeleteByName(ctx, "TestDelete"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	matched, _, _ := repo.Match(ctx, emb, repository.EuclideanMatchThreshold)
	if matched.Name != "" {
		t.Errorf("after delete, match = %q, want empty", matched.Name)
	}
}

func TestPersonRepoGetByName(t *testing.T) {
	q := testDB(t)
	ctx := context.Background()
	repo := repository.NewPersonRepo(q)

	emb := make([]float32, 128)
	emb[0] = 1.0
	_, err := repo.Create(ctx, "TestLookup", [][]float32{emb}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { repo.DeleteByName(ctx, "TestLookup") })

	p, err := repo.GetByName(ctx, "TestLookup")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.Name != "TestLookup" {
		t.Errorf("name = %q, want TestLookup", p.Name)
	}
	if len(p.Embeddings[0]) != 128 {
		t.Errorf("embedding length = %d, want 128", len(p.Embeddings[0]))
	}
}

func TestParseEmbeddings_MultiPoseShape(t *testing.T) {
	a := makeEmbedding(0.1)
	b := makeEmbedding(0.2)
	c := makeEmbedding(0.3)
	raw, err := json.Marshal([][]float32{a, b, c})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := repository.ParseEmbeddings(raw)
	if err != nil {
		t.Fatalf("parseEmbeddings: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 embeddings, got %d", len(got))
	}
	for i, want := range [][]float32{a, b, c} {
		if !floatSliceEq(got[i], want) {
			t.Errorf("pose %d mismatch", i)
		}
	}
}

func TestParseEmbeddings_LegacyFlatShape(t *testing.T) {
	a := makeEmbedding(0.5)
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := repository.ParseEmbeddings(raw)
	if err != nil {
		t.Fatalf("parseEmbeddings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 embedding (legacy promoted), got %d", len(got))
	}
	if !floatSliceEq(got[0], a) {
		t.Error("legacy embedding round-trip mismatch")
	}
}

// TestPersonMatch_BestOfThreePoses confirms a probe close to one of the
// stored poses matches the person, even when the other two poses are far
// from the probe.
func TestPersonMatch_BestOfThreePoses(t *testing.T) {
	q := testDB(t)
	ctx := context.Background()
	repo := repository.NewPersonRepo(q)

	front := makeEmbedding(0.1)
	left := makeEmbedding(0.7)
	right := makeEmbedding(1.3)
	_, err := repo.Create(ctx, "BestOfThreePerson", [][]float32{front, left, right}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { repo.DeleteByName(ctx, "BestOfThreePerson") })

	probe := makeEmbedding(1.31)
	got, dist, err := repo.Match(ctx, probe, repository.EuclideanMatchThreshold)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got.Name != "BestOfThreePerson" {
		t.Fatalf("want BestOfThreePerson, got %q (dist=%f)", got.Name, dist)
	}
}

func makeEmbedding(seed float32) []float32 {
	out := make([]float32, 128)
	for i := range out {
		out[i] = seed + float32(i)/1000
	}
	return out
}

func floatSliceEq(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
