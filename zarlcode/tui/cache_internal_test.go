package tui

import (
	"reflect"
	"strings"
	"testing"
)

// spyItem counts how many times its underlying render runs, so tests
// can assert caching/freeze/viewport-bounding behaviour.
type spyItem struct {
	versioned
	body    string
	fin     bool
	renders int
}

func (s *spyItem) render(int) []string { s.renders++; return []string{s.body} }
func (s *spyItem) finished() bool      { return s.fin }

func TestRenderItem_CachesByVersionAndWidth(t *testing.T) {
	tl := newTimeline()
	it := &spyItem{body: "hello"}

	tl.renderItem(it, 80)
	tl.renderItem(it, 80)
	if it.renders != 1 {
		t.Fatalf("stable (width,version) should render once, got %d", it.renders)
	}

	it.body = "changed"
	it.bump()
	tl.renderItem(it, 80)
	if it.renders != 2 {
		t.Fatalf("version bump should re-render, got %d", it.renders)
	}

	tl.renderItem(it, 100)
	if it.renders != 3 {
		t.Fatalf("width change should re-render, got %d", it.renders)
	}
}

func TestRenderItem_CacheHitIsByteIdentical(t *testing.T) {
	tl := newTimeline()
	it := &spyItem{body: "stable"}
	first := tl.renderItem(it, 40)
	again := tl.renderItem(it, 40)
	if !reflect.DeepEqual(first, again) {
		t.Fatalf("cache hit differs from first render: %v vs %v", first, again)
	}
}

func TestFinishedItem_FreezesAfterFirstRender(t *testing.T) {
	tl := newTimeline()
	it := &spyItem{body: "done", fin: true}
	for range 5 {
		tl.renderItem(it, 60)
	}
	if it.renders != 1 {
		t.Fatalf("finished item should render once then freeze, got %d", it.renders)
	}
}

func TestRenderViewport_OnlyRendersVisible(t *testing.T) {
	tl := newTimeline()
	for range 50 {
		tl.items = append(tl.items, &spyItem{body: "x", fin: true})
	}
	tl.renderViewport(80, 10)

	if top := tl.items[0].(*spyItem); top.renders != 0 {
		t.Fatalf("off-screen top item rendered %d times — viewport not bounded", top.renders)
	}
	if bot := tl.items[49].(*spyItem); bot.renders != 1 {
		t.Fatalf("bottom item should render once, got %d", bot.renders)
	}
}

func TestRenderTailInvisibleItemsDoNotHideHistory(t *testing.T) {
	tl := newTimeline()
	tl.addUser("oldest visible message")
	for range 20 {
		// Every conversation turn creates this placeholder before a skill is
		// loaded. It renders no rows but remains in the timeline.
		tl.appendItem(&skillsItem{nested: true})
	}
	tl.addUser("newest visible message")

	got := strings.Join(tl.renderViewport(80, 5), "\n")
	for _, want := range []string{"newest visible message", "└─"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tail lost %q behind invisible items:\n%s", want, got)
		}
	}
}
