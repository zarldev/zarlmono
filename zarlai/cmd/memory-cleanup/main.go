// memory-cleanup is an interactive triage tool for the per-person fact
// store in Qdrant. It scrolls every memory for the named person, prints a
// numbered list, and lets the operator delete by index list — useful when
// the auto-extractor has accumulated noise.
//
// Usage:
//
//	go run ./cmd/memory-cleanup -person <name> [-qdrant http://localhost:6333]
//
// At the prompt: a comma-separated index list ("3,7,12-18"), `all` to
// nuke everything for the person, or empty / `q` to quit. Always confirms
// before deleting.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

func main() {
	person := flag.String("person", "", "person name to clean up memories for (required)")
	qdrantURL := flag.String("qdrant", "http://localhost:6333", "qdrant base URL")
	flag.Parse()
	if *person == "" {
		flag.Usage()
		log.Fatal("-person is required")
	}

	ctx := context.Background()
	c := qdrant.NewClient(*qdrantURL)

	memories, err := loadAll(ctx, c, *person)
	if err != nil {
		log.Fatalf("load memories: %v", err)
	}
	if len(memories) == 0 {
		fmt.Printf("No memories for %s.\n", *person)
		return
	}

	sort.Slice(memories, func(i, j int) bool { return memories[i].createdAt.After(memories[j].createdAt) })

	for {
		printList(*person, memories)
		fmt.Print("\nDelete which? (e.g. 3,7,12-18 | all | q): ")
		line, ok := readLine()
		if !ok || line == "" || line == "q" {
			return
		}

		var victims []int
		if line == "all" {
			victims = make([]int, len(memories))
			for i := range memories {
				victims[i] = i
			}
		} else {
			parsed, err := parseSelection(line, len(memories))
			if err != nil {
				fmt.Printf("  parse error: %v\n", err)
				continue
			}
			victims = parsed
		}
		if len(victims) == 0 {
			continue
		}

		fmt.Printf("\nWill delete %d memories:\n", len(victims))
		for _, i := range victims {
			fmt.Printf("  - %s\n", truncate(memories[i].fact, 100))
		}
		fmt.Print("Confirm? [y/N]: ")
		conf, _ := readLine()
		if strings.ToLower(strings.TrimSpace(conf)) != "y" {
			fmt.Println("aborted")
			continue
		}

		deleted := 0
		for _, i := range victims {
			if err := c.DeleteByID(ctx, memory.Collection, memories[i].id); err != nil {
				fmt.Printf("  delete %s failed: %v\n", memories[i].id, err)
				continue
			}
			deleted++
		}
		fmt.Printf("deleted %d/%d\n", deleted, len(victims))

		// Drop deleted entries from the in-memory list and re-renumber.
		survivor := make([]memEntry, 0, len(memories)-deleted)
		victimSet := make(map[int]bool, len(victims))
		for _, i := range victims {
			victimSet[i] = true
		}
		for i, m := range memories {
			if !victimSet[i] {
				survivor = append(survivor, m)
			}
		}
		memories = survivor
		if len(memories) == 0 {
			fmt.Println("nothing left.")
			return
		}
	}
}

type memEntry struct {
	id        string
	fact      string
	createdAt time.Time
}

func loadAll(ctx context.Context, c *qdrant.Client, person string) ([]memEntry, error) {
	filter := &qdrant.Filter{
		Must: []qdrant.FieldCondition{
			{Key: "person_name", Match: qdrant.MatchValue{Value: person}},
		},
	}
	var all []memEntry
	var offset any
	for {
		points, next, err := c.Scroll(ctx, qdrant.ScrollRequest{
			Collection: memory.Collection,
			Filter:     filter,
			Limit:      256,
			Offset:     offset,
		})
		if err != nil {
			return nil, err
		}
		for _, p := range points {
			fact, _ := p.Payload["fact"].(string)
			createdRaw, _ := p.Payload["created_at"].(string)
			createdAt, _ := time.Parse(time.RFC3339, createdRaw)
			all = append(all, memEntry{id: p.ID, fact: fact, createdAt: createdAt})
		}
		if next == nil {
			break
		}
		offset = next
	}
	return all, nil
}

func printList(person string, memories []memEntry) {
	fmt.Printf("\n=== %d memories for %s ===\n", len(memories), person)
	now := time.Now()
	for i, m := range memories {
		age := "—"
		if !m.createdAt.IsZero() {
			age = formatAge(now.Sub(m.createdAt))
		}
		fmt.Printf("%4d  %-7s  %s\n", i, age, truncate(m.fact, 140))
	}
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// parseSelection turns "3,7,12-18,20" into a sorted, deduplicated index
// slice. Validates each index against [0,max).
func parseSelection(s string, max int) ([]int, error) {
	seen := map[int]bool{}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if dash := strings.Index(part, "-"); dash > 0 {
			lo, err1 := strconv.Atoi(strings.TrimSpace(part[:dash]))
			hi, err2 := strconv.Atoi(strings.TrimSpace(part[dash+1:]))
			if err1 != nil || err2 != nil || lo > hi {
				return nil, fmt.Errorf("bad range %q", part)
			}
			for i := lo; i <= hi; i++ {
				if i < 0 || i >= max {
					return nil, fmt.Errorf("index %d out of range", i)
				}
				seen[i] = true
			}
			continue
		}
		i, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("bad number %q", part)
		}
		if i < 0 || i >= max {
			return nil, fmt.Errorf("index %d out of range", i)
		}
		seen[i] = true
	}
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out, nil
}

func readLine() (string, bool) {
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(line), true
}
