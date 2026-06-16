package skills_test

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/skills"
)

// Load the initial set, read a snapshot, mutate, observe the version
// bump that downstream caches key off.
func ExampleMemorySkillStore() {
	store := skills.NewMemorySkillStore()
	store.Load([]skills.Skill{
		{ID: "git", Name: "git", Markdown: "# Git\n..."},
	})
	v0 := store.Version()
	got := store.EnabledSkills()

	store.Load([]skills.Skill{
		{ID: "git", Name: "git", Markdown: "# Git\n..."},
		{ID: "search", Name: "search", Markdown: "# Search\n..."},
	})
	v1 := store.Version()

	fmt.Println(len(got), v1 > v0)
	// Output: 1 true
}

// AddBumper registers a downstream invalidation target. On every
// Load, the store calls BumpVersion on each registered bumper.
func ExampleMemorySkillStore_addBumper() {
	store := skills.NewMemorySkillStore()
	cache := &countingBumper{}
	store.AddBumper(cache)

	store.Load([]skills.Skill{{ID: "x", Name: "x"}})
	store.Load([]skills.Skill{{ID: "x", Name: "x"}})

	fmt.Println(cache.bumps)
	// Output: 2
}

type countingBumper struct{ bumps int }

func (c *countingBumper) BumpVersion() { c.bumps++ }
