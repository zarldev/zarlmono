package diffrecorder

import (
	"fmt"
	"strconv"
	"strings"
)

const diffContextLines = 3

// unifiedDiff returns a textual unified diff between before and after
// for path. Returns "" when nothing observable changed (both missing,
// or content matches).
//
// The format mirrors a plain `diff -u`: an "@@ <path> @@" file header
// followed by " "/"-"/"+"-prefixed lines. The Files pane in the
// cockpit picks up the prefixes for syntax-style colouring.
func unifiedDiff(path string, before []byte, beforeMissing bool, after []byte, afterMissing bool) string {
	switch {
	case beforeMissing && afterMissing:
		return ""
	case beforeMissing:
		return renderDiff(path, "", string(after))
	case afterMissing:
		return renderDiff(path, string(before), "")
	default:
		if string(before) == string(after) {
			return ""
		}
		return renderDiff(path, string(before), string(after))
	}
}

func renderDiff(path, before, after string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@@ %s @@\n", path)

	beforeLines := splitLines(before)
	afterLines := splitLines(after)
	script := diffScript(beforeLines, afterLines)

	for _, h := range compactHunks(script, diffContextLines) {
		oldStart, oldCount, newStart, newCount := hunkRange(script, h.start, h.end)
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", formatRange(oldStart, oldCount), formatRange(newStart, newCount))
		for _, ln := range script[h.start:h.end] {
			fmt.Fprintf(&b, "%c%s\n", ln.op, ln.text)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type diffLine struct {
	op   byte
	text string
}

type diffHunk struct {
	start int
	end   int
}

func diffScript(beforeLines, afterLines []string) []diffLine {
	lcs := longestCommonSubseq(beforeLines, afterLines)

	script := make([]diffLine, 0, len(beforeLines)+len(afterLines))
	i, j, k := 0, 0, 0
	for i < len(beforeLines) || j < len(afterLines) {
		switch {
		case k < len(lcs) && i < len(beforeLines) && beforeLines[i] == lcs[k] && j < len(afterLines) && afterLines[j] == lcs[k]:
			script = append(script, diffLine{op: ' ', text: beforeLines[i]})
			i++
			j++
			k++
		case i < len(beforeLines) && (k >= len(lcs) || beforeLines[i] != lcs[k]):
			script = append(script, diffLine{op: '-', text: beforeLines[i]})
			i++
		case j < len(afterLines) && (k >= len(lcs) || afterLines[j] != lcs[k]):
			script = append(script, diffLine{op: '+', text: afterLines[j]})
			j++
		}
	}
	return script
}

func compactHunks(script []diffLine, contextLines int) []diffHunk {
	if contextLines < 0 {
		contextLines = 0
	}
	var hunks []diffHunk
	for i := 0; i < len(script); {
		for i < len(script) && script[i].op == ' ' {
			i++
		}
		if i >= len(script) {
			break
		}

		start := max(i-contextLines, 0)
		end := min(i+contextLines+1, len(script))

		i++
		for {
			for i < len(script) && script[i].op == ' ' {
				i++
			}
			if i >= len(script) || i > end+contextLines {
				break
			}
			end = min(i+contextLines+1, len(script))
			i++
		}

		hunks = append(hunks, diffHunk{start: start, end: end})
	}
	return hunks
}

func hunkRange(script []diffLine, start, end int) (int, int, int, int) {
	var oldStart, newStart int
	oldCount, newCount := 0, 0
	oldBefore, newBefore := 0, 0
	for i := range start {
		switch script[i].op {
		case ' ':
			oldBefore++
			newBefore++
		case '-':
			oldBefore++
		case '+':
			newBefore++
		}
	}
	for i := start; i < end; i++ {
		switch script[i].op {
		case ' ':
			oldCount++
			newCount++
		case '-':
			oldCount++
		case '+':
			newCount++
		}
	}
	oldStart = hunkStart(oldBefore, oldCount)
	newStart = hunkStart(newBefore, newCount)
	return oldStart, oldCount, newStart, newCount
}

func hunkStart(before, count int) int {
	if count == 0 {
		return before
	}
	return before + 1
}

func formatRange(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// splitLines normalises s into lines without trailing newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimRight(s, "\n")
	return strings.Split(s, "\n")
}

// longestCommonSubseq returns the LCS of two string slices. Standard
// O(n*m) DP — adequate for the small file sizes a single tool call
// touches.
func longestCommonSubseq(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			switch {
			case a[i-1] == b[j-1]:
				dp[i][j] = dp[i-1][j-1] + 1
			case dp[i-1][j] >= dp[i][j-1]:
				dp[i][j] = dp[i-1][j]
			default:
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	out := make([]string, 0, dp[len(a)][len(b)])
	i, j := len(a), len(b)
	for i > 0 && j > 0 {
		switch {
		case a[i-1] == b[j-1]:
			out = append([]string{a[i-1]}, out...)
			i--
			j--
		case dp[i-1][j] >= dp[i][j-1]:
			i--
		default:
			j--
		}
	}
	return out
}
