package board_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/board"
)

func draft(t *testing.T, summary, body, topic string, reply board.ID) board.Draft {
	t.Helper()
	d, err := board.ParseDraft(summary, body, topic, reply)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBoardOrderingReplyPaginationAndSnapshots(t *testing.T) {
	s := board.New(board.DefaultLimits())
	for index := range 3 {
		_, err := s.Append("researcher", draft(t, fmt.Sprintf("finding %d", index), "private body", "topic", board.ID(index)))
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.List(0, 2, "")
	if err != nil || len(page.Posts) != 2 || page.Next != 2 || !page.More {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	page.Posts[0].Summary = "changed by caller"
	posts, err := s.Read([]board.ID{3, 1})
	if err != nil || posts[0].ReplyTo != 2 || posts[1].Summary.Summary != "finding 0" {
		t.Fatalf("read = %+v, %v", posts, err)
	}
	posts[0].Body = "changed by caller"
	again, _ := s.Read([]board.ID{3})
	if again[0].Body != "private body" {
		t.Fatal("read retained a mutable alias")
	}
	filtered, err := s.List(0, 1, "absent")
	if err != nil || len(filtered.Posts) != 0 || filtered.Next != 3 || filtered.More {
		t.Fatalf("filtered page = %+v, %v", filtered, err)
	}
	last, err := s.List(page.Next, 2, "")
	if err != nil || len(last.Posts) != 1 || last.Posts[0].ID != 3 || last.More {
		t.Fatalf("last page = %+v, %v", last, err)
	}
}

func TestBoardValidationAndAtomicCapacity(t *testing.T) {
	for _, body := range []string{"", "  ", strings.Repeat("x", board.MaxBodyBytes+1), string([]byte{0xff})} {
		if _, err := board.ParseDraft("summary", body, "", 0); !errors.Is(err, board.ErrInvalidDraft) {
			t.Fatalf("body validation = %v", err)
		}
	}
	s := board.New(board.Limits{Posts: 1, Bytes: 1024})
	if _, err := s.Append("a", draft(t, "bad reply", "body", "", 9)); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("unknown reply = %v", err)
	}
	post, err := s.Append("a", draft(t, "summary", "body", "", 0))
	if err != nil || post.ID != 1 {
		t.Fatalf("first accepted identity = %d, %v", post.ID, err)
	}
	if _, err := s.Append("a", draft(t, "overflow", "body", "", 1)); !errors.Is(err, board.ErrCapacity) {
		t.Fatalf("capacity = %v", err)
	}
	if _, err := s.Read([]board.ID{1, 2}); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("partial read = %v", err)
	}
	if _, err := s.List(2, 1, ""); !errors.Is(err, board.ErrInvalidPage) {
		t.Fatalf("future cursor = %v", err)
	}
}

func TestBoardRejectsWideIDsBeforeIndexing(t *testing.T) {
	t.Parallel()
	store := board.New(board.DefaultLimits())
	post, err := store.Append("author", draft(t, "finding", "evidence", "", 0))
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.List(post.ID, 1, "")
	if err != nil || len(page.Posts) != 0 || page.Next != post.ID || page.More {
		t.Fatalf("cursor at retained boundary = %+v, %v", page, err)
	}
	for _, id := range []board.ID{2, 1 << 63, ^board.ID(0)} {
		if _, err := store.List(id, 1, ""); !errors.Is(err, board.ErrInvalidPage) {
			t.Errorf("cursor %d: %v", id, err)
		}
		posts, err := store.Read([]board.ID{post.ID, id})
		if !errors.Is(err, board.ErrNotFound) || len(posts) != 0 {
			t.Errorf("read batch with %d = %+v, %v", id, posts, err)
		}
	}
}

func TestConcurrentAppendAllocatesContiguousIDs(t *testing.T) {
	s := board.New(board.DefaultLimits())
	d := draft(t, "finding", "body", "", 0)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if _, err := s.Append("a", d); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	page, err := s.List(0, 32, "")
	if err != nil || len(page.Posts) != 32 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	for index, post := range page.Posts {
		if post.ID != board.ID(index+1) {
			t.Fatalf("post %d ID = %d", index, post.ID)
		}
	}
}
