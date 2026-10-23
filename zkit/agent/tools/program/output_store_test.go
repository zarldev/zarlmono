package program_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestOutputStorePagesAndOwnership(t *testing.T) {
	store := program.NewOutputStore()
	t.Cleanup(store.Close)
	data := []byte(`"a🙂é中\n"`)
	want := string(data)
	receipt, err := store.Retain(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	clear(data)
	var got strings.Builder
	offset := 0
	for {
		page, err := store.Read(t.Context(), program.OutputReadArgs{Receipt: receipt, Offset: offset, Limit: 4})
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(page.Content) || page.NextOffset <= offset || page.TotalBytes != len(want) {
			t.Fatalf("invalid page: %+v", page)
		}
		got.WriteString(page.Content)
		offset = page.NextOffset
		if page.Done {
			break
		}
	}
	if got.String() != want {
		t.Fatalf("pages = %q, want %q", got.String(), want)
	}
	page, err := store.Read(t.Context(), program.OutputReadArgs{Receipt: receipt, Offset: offset})
	if err != nil || !page.Done || page.Content != "" {
		t.Fatalf("EOF = %+v, %v", page, err)
	}
	for _, args := range []program.OutputReadArgs{
		{Receipt: receipt, Offset: -1}, {Receipt: receipt, Offset: 3},
		{Receipt: receipt, Offset: len(want) + 1}, {Receipt: receipt, Limit: -1},
		{Receipt: receipt, Limit: 3}, {Receipt: receipt, Limit: 16385},
	} {
		_, err := store.Read(t.Context(), args)
		assertOutputErrorKind(t, err, tools.Kinds.VALIDATION)
	}
	store.Close()
	if page.Content != "" || got.String() != want {
		t.Fatal("revocation changed copied pages")
	}
	_, err = store.Retain(t.Context(), []byte(`"late"`))
	assertOutputErrorKind(t, err, tools.Kinds.NOTFOUND)
}

func TestOutputStoreBoundsAndExpiry(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size, count int
	}{
		{"entries", 100, 32}, {"aggregate", 1 << 20, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := program.NewOutputStore()
			t.Cleanup(store.Close)
			data := []byte(`"` + strings.Repeat("x", tc.size-2) + `"`)
			var receipts []string
			for range tc.count + 1 {
				receipt, err := store.Retain(t.Context(), data)
				if err != nil {
					t.Fatal(err)
				}
				receipts = append(receipts, receipt)
			}
			_, err := store.Read(t.Context(), program.OutputReadArgs{Receipt: receipts[0]})
			assertOutputErrorKind(t, err, tools.Kinds.NOTFOUND)
			for _, receipt := range receipts[1:] {
				page, err := store.Read(t.Context(), program.OutputReadArgs{Receipt: receipt})
				if err != nil || len(page.Content) > 8192 {
					t.Fatalf("retained page = %+v, %v", page, err)
				}
			}
			_, err = store.Retain(t.Context(), bytes.Repeat([]byte("x"), (1<<20)+1))
			assertOutputErrorKind(t, err, tools.Kinds.BUDGET)
			if _, err := store.Read(t.Context(), program.OutputReadArgs{Receipt: receipts[1]}); err != nil {
				t.Fatal("oversized insertion evicted valid entry", err)
			}
		})
	}
	synctest.Test(t, func(t *testing.T) {
		store := program.NewOutputStore()
		t.Cleanup(store.Close)
		receipt, err := store.Retain(t.Context(), []byte(`"secret"`))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(15*time.Minute - time.Nanosecond)
		if _, err := store.Read(t.Context(), program.OutputReadArgs{Receipt: receipt}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Nanosecond)
		_, expired := store.Read(t.Context(), program.OutputReadArgs{Receipt: receipt})
		_, unknown := store.Read(t.Context(), program.OutputReadArgs{Receipt: "unknown"})
		assertOutputErrorKind(t, expired, tools.Kinds.NOTFOUND)
		if expired.Error() != unknown.Error() {
			t.Fatal("expiry disclosed receipt state")
		}
	})
}

func TestOutputStoreIsolationCancellationAndClose(t *testing.T) {
	first, second := program.NewOutputStore(), program.NewOutputStore()
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	receipt, err := first.Retain(t.Context(), []byte(`"canary-secret"`))
	if err != nil {
		t.Fatal(err)
	}
	_, other := second.Read(t.Context(), program.OutputReadArgs{Receipt: receipt})
	first.Close()
	_, closed := first.Read(t.Context(), program.OutputReadArgs{Receipt: receipt})
	if other.Error() != closed.Error() || strings.Contains(closed.Error(), "canary") {
		t.Fatal("unavailable results differ or leak data")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := second.Retain(ctx, []byte(`"cancelled"`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("retain cancellation = %v", err)
	}
	if _, err := second.Read(ctx, program.OutputReadArgs{Receipt: receipt}); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation = %v", err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			id, err := second.Retain(t.Context(), []byte(`"concurrent"`))
			if err == nil {
				_, _ = second.Read(t.Context(), program.OutputReadArgs{Receipt: id})
			}
		})
	}
	second.Close()
	wg.Wait()
	_, err = second.Retain(t.Context(), []byte(`"late"`))
	assertOutputErrorKind(t, err, tools.Kinds.NOTFOUND)
}

func assertOutputErrorKind(t *testing.T, err error, kind tools.Kind) {
	t.Helper()
	typed, ok := errors.AsType[*tools.Error](err)
	if !ok || typed.Kind != kind {
		t.Fatalf("error = %v, want %v", err, kind)
	}
}
