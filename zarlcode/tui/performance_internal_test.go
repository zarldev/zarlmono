package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	benchmarkTimelineWidth  = 80
	benchmarkTimelineHeight = 30
)

func benchmarkTimeline(itemCount int) *timeline {
	tl := newTimeline()
	for i := range itemCount {
		tl.addUser("user message " + strconv.Itoa(i) + " with enough text to wrap across the transcript viewport")
	}
	tl.renderViewport(benchmarkTimelineWidth, benchmarkTimelineHeight)
	return tl
}

func BenchmarkTimelineCursor(b *testing.B) {
	for _, itemCount := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(itemCount), func(b *testing.B) {
			tl := benchmarkTimeline(itemCount)
			tl.cursorTop()
			b.ResetTimer()
			for range b.N {
				tl.cursorDown()
				if !tl.browsing {
					tl.cursorTop()
				}
			}
		})
	}
}

func BenchmarkTimelinePage(b *testing.B) {
	for _, itemCount := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(itemCount), func(b *testing.B) {
			tl := benchmarkTimeline(itemCount)
			tl.cursorTop()
			b.ResetTimer()
			for range b.N {
				tl.pageDown()
				if !tl.browsing {
					tl.cursorTop()
				}
			}
		})
	}
}

func BenchmarkTimelineSelection(b *testing.B) {
	for _, itemCount := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(itemCount), func(b *testing.B) {
			tl := benchmarkTimeline(itemCount)
			tl.cursorTop()
			b.ResetTimer()
			for range b.N {
				tl.scrollToSel()
			}
		})
	}
}

func BenchmarkTimelineRenderTail(b *testing.B) {
	tl := benchmarkTimeline(10000)
	b.ResetTimer()
	for range b.N {
		tl.renderViewport(benchmarkTimelineWidth, benchmarkTimelineHeight)
	}
}

func BenchmarkTimelineRenderBrowse(b *testing.B) {
	tl := benchmarkTimeline(10000)
	tl.cursorTop()
	tl.scrollToFraction(0.5)
	b.ResetTimer()
	for range b.N {
		tl.renderViewport(benchmarkTimelineWidth, benchmarkTimelineHeight)
	}
}

func BenchmarkContentHotCache(b *testing.B) {
	for _, size := range []int{1 << 10, 100 << 10, 1 << 20} {
		b.Run(strconv.Itoa(size)+"B/markdown", func(b *testing.B) {
			contentRenderCache.reset()
			block := contentBlock{
				kind:     contentMarkdown,
				text:     strings.Repeat("# heading\n\nparagraph content\n\n", size/29+1)[:size],
				cacheKey: "benchmark-markdown-" + strconv.Itoa(size),
				revision: 1,
			}
			renderContentBlock(benchmarkTimelineWidth, block)
			b.ResetTimer()
			for range b.N {
				renderContentBlock(benchmarkTimelineWidth, block)
			}
		})
		b.Run(strconv.Itoa(size)+"B/tool-result", func(b *testing.B) {
			contentRenderCache.reset()
			block := contentBlock{
				kind:     contentToolResult,
				text:     strings.Repeat("package example\nfunc Example() {}\n", size/33+1)[:size],
				toolName: "read",
				cacheKey: "benchmark-tool-result-" + strconv.Itoa(size),
				revision: 1,
			}
			renderContentBlock(benchmarkTimelineWidth, block)
			b.ResetTimer()
			for range b.N {
				renderContentBlock(benchmarkTimelineWidth, block)
			}
		})
	}
}

func BenchmarkCockpitHistory(b *testing.B) {
	s := RunState{history: make([]turnSample, historyCap)}
	for i := range s.history {
		s.history[i] = turnSample{
			fillFrac:  float64(i) / historyCap,
			tokIn:     1000 + i,
			cached:    i,
			tokPerSec: float64(i),
			costUSD:   float64(i) / 1000,
		}
	}
	b.ResetTimer()
	for range b.N {
		s.tpsSeries()
		s.cacheSeries()
		s.costSeries()
	}
}

func BenchmarkCockpitTopTools(b *testing.B) {
	s := RunState{toolStats: make(map[string]toolStat, 1000)}
	for i := range 1000 {
		s.toolStats["tool-"+strconv.Itoa(i)] = toolStat{
			calls: i % 100,
			fails: i % 7,
			dur:   time.Duration(i) * time.Millisecond,
		}
	}
	b.ResetTimer()
	for range b.N {
		s.topTools()
	}
}
