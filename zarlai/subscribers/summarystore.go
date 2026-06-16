package subscribers

import (
	"context"

	"github.com/zarldev/zarlmono/zarlai/repository"
)

// RepoSummaryStore implements SummaryStore using the conversation summary repository.
type RepoSummaryStore struct {
	repo *repository.ConversationSummaryRepo
}

func NewRepoSummaryStore(repo *repository.ConversationSummaryRepo) *RepoSummaryStore {
	return &RepoSummaryStore{repo: repo}
}

func (s *RepoSummaryStore) CreateSummary(ctx context.Context, personName, summary, sessionID string) error {
	_, err := s.repo.Create(ctx, personName, summary, sessionID)
	return err
}
