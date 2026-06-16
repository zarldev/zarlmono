package skills_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/skills"
)

func TestMemorySkillStore_LoadAndEnabledSkills(t *testing.T) {
	t.Parallel()

	s := skills.NewMemorySkillStore()
	if got := s.EnabledSkills(); len(got) != 0 {
		t.Errorf("empty store returned %d skills, want 0", len(got))
	}

	skills := []skills.Skill{
		{ID: "1", Name: "calculate", Description: "math", Markdown: "# Math\nUse for arithmetic"},
		{ID: "2", Name: "search", Description: "web search", Markdown: "# Search\nFor current info"},
	}
	s.Load(skills)

	got := s.EnabledSkills()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "calculate" || got[1].Name != "search" {
		t.Errorf("names = %q,%q", got[0].Name, got[1].Name)
	}
}

func TestMemorySkillStore_VersionBumpsOnLoad(t *testing.T) {
	t.Parallel()

	s := skills.NewMemorySkillStore()
	v0 := s.Version()
	s.Load([]skills.Skill{{ID: "1", Name: "x"}})
	if s.Version() <= v0 {
		t.Errorf("Version did not bump after Load (was %d, now %d)", v0, s.Version())
	}
	v1 := s.Version()
	s.Load([]skills.Skill{{ID: "1", Name: "x"}, {ID: "2", Name: "y"}})
	if s.Version() <= v1 {
		t.Errorf("Version did not bump on second Load (was %d, now %d)", v1, s.Version())
	}
}

type recordingBumper struct{ bumps int }

func (r *recordingBumper) BumpVersion() { r.bumps++ }

func TestMemorySkillStore_BumpsRegisteredCaches(t *testing.T) {
	t.Parallel()

	s := skills.NewMemorySkillStore()
	b := &recordingBumper{}
	s.AddBumper(b)

	s.Load([]skills.Skill{{ID: "1"}})
	if b.bumps != 1 {
		t.Errorf("bumps = %d, want 1", b.bumps)
	}
	s.Load([]skills.Skill{{ID: "1"}, {ID: "2"}})
	if b.bumps != 2 {
		t.Errorf("bumps = %d, want 2", b.bumps)
	}
}

func TestMemorySkillStore_EnabledSkillsReturnsCopy(t *testing.T) {
	t.Parallel()

	s := skills.NewMemorySkillStore()
	s.Load([]skills.Skill{{ID: "1", Name: "original"}})

	got := s.EnabledSkills()
	got[0].Name = "mutated"

	again := s.EnabledSkills()
	if again[0].Name != "original" {
		t.Errorf("EnabledSkills returned reference, not copy — store state was mutated")
	}
}
