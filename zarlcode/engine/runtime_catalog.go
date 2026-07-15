package engine

import (
	"slices"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
)

// RuntimeCatalog is the v2 live-run catalogue: a concurrency-safe snapshot of
// the skills, named sub-agents, and command hooks discoverable from the
// current workspace. It is
// intentionally backed by the charm-free zarlcode/catalog package (not the v1
// tui package) so v2 can share the on-disk format without importing bubbletea v1.
type RuntimeCatalog struct {
	mu sync.RWMutex

	wsRoot string
	skills []catalog.Skill
	agents []catalog.Agent
	hooks  []catalog.Hook

	skillByName map[string]catalog.Skill
	agentByName map[string]catalog.Agent
	lastErrs    []error
}

func newRuntimeCatalog(wsRoot string) *RuntimeCatalog {
	c := &RuntimeCatalog{}
	c.Reload(wsRoot)
	return c
}

// Reload re-reads the skill/agent directories. Individual malformed files are
// collected and retained, but valid entries still replace the previous snapshot.
func (c *RuntimeCatalog) Reload(wsRoot string) []error {
	if c == nil {
		return nil
	}
	skills, skillErrs := catalog.LoadSkills(wsRoot)
	agents, agentErrs := catalog.LoadAgents(wsRoot)
	hooks, hookErrs := catalog.LoadHooks(wsRoot)

	skillByName := make(map[string]catalog.Skill, len(skills))
	for _, s := range skills {
		skillByName[s.Name] = s
	}
	agentByName := make(map[string]catalog.Agent, len(agents))
	for _, a := range agents {
		agentByName[a.Name] = a
	}
	errs := append(append(append([]error{}, skillErrs...), agentErrs...), hookErrs...)

	c.mu.Lock()
	c.wsRoot = wsRoot
	c.skills = append([]catalog.Skill(nil), skills...)
	c.agents = append([]catalog.Agent(nil), agents...)
	c.hooks = append([]catalog.Hook(nil), hooks...)
	c.skillByName = skillByName
	c.agentByName = agentByName
	c.lastErrs = errs
	c.mu.Unlock()
	return errs
}

// ReloadCurrent re-reads the catalogue from its last configured workspace root.
func (c *RuntimeCatalog) ReloadCurrent() []error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	wsRoot := c.wsRoot
	c.mu.RUnlock()
	return c.Reload(wsRoot)
}

func (c *RuntimeCatalog) Errors() []error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]error(nil), c.lastErrs...)
}

func (c *RuntimeCatalog) Skills() []catalog.Skill {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]catalog.Skill(nil), c.skills...)
}

func (c *RuntimeCatalog) Hooks() []catalog.Hook {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]catalog.Hook(nil), c.hooks...)
}

func (c *RuntimeCatalog) Agents() []catalog.Agent {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]catalog.Agent(nil), c.agents...)
}

func (c *RuntimeCatalog) Skill(name string) (catalog.Skill, bool) {
	if c == nil {
		return catalog.Skill{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.skillByName[strings.TrimSpace(name)]
	return s, ok
}

func (c *RuntimeCatalog) Agent(name string) (catalog.Agent, bool) {
	if c == nil {
		return catalog.Agent{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	a, ok := c.agentByName[strings.TrimSpace(name)]
	return a, ok
}

func (c *RuntimeCatalog) SkillNames() []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.skillByName))
	for n := range c.skillByName {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func (c *RuntimeCatalog) AgentNames() []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.agentByName))
	for n := range c.agentByName {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Lookup adapts RuntimeCatalog to guardrails.SkillLookup. By convention, a skill
// named after a tool ("edit", "bash", etc.) is a recovery recipe for failures
// from that tool.
func (c *RuntimeCatalog) Lookup(toolName string) (string, bool) {
	s, ok := c.Skill(toolName)
	if !ok {
		return "", false
	}
	return s.Source, true
}
