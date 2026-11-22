package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func newCacheEditorUI(t *testing.T) (*engine.Settings, *tui.UI, *engine.LiveRunner, engine.ProviderSpec) {
	t.Helper()
	s, ui, live, spec := newProtocolSelectionUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(routeEditorDefinitions()), backends.WithRouteProtocolStore(s.Store), backends.WithRouteCachePolicyStore(s.Store))
	step(t, ui, routeManualBuild(t, ui, spec.Name, spec.Model)())
	step(t, ui, window(170, 50))
	return s, ui, live, spec
}
func openCacheEditor(t *testing.T, ui *tui.UI, custom bool) {
	t.Helper()
	openSettings(t, ui)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	if custom {
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	step(t, ui, textKey("c"))
	requireCacheView(t, ui, "Route cache markers")
}
func requireCacheView(t *testing.T, ui *tui.UI, want string) {
	t.Helper()
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, want) {
		t.Fatalf("view missing %q:\n%s", want, view)
	}
}
func cacheChoice(t *testing.T, ui *tui.UI, n int) {
	t.Helper()
	for range n {
		step(t, ui, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
}
func closeCacheEditor(t *testing.T, ui *tui.UI) {
	t.Helper()
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEscape})
	closeOpenSettings(t, ui)
}

func TestRouteCacheEditorSaveClearAndReopen(t *testing.T) {
	s, ui, live, spec := newCacheEditorUI(t)
	before := live.RunTarget().Provider
	beforeSpec := live.RunTarget().Spec
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireCacheView(t, ui, "Not configured; factory default inherited")
	cacheChoice(t, ui, 1)
	saved, err := s.Registry.GetRouteCachePolicy(t.Context(), spec.Name, spec.Model)
	if err != nil || saved.Mode != backends.RouteCacheModes.CACHEDEFAULT {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	if live.RunTarget().Provider != before {
		t.Fatal("save applied before settings close")
	}
	closeCacheEditor(t, ui)
	if live.RunTarget().Provider == before {
		t.Fatal("refresh did not rebuild")
	}
	if live.RunTarget().Spec != beforeSpec || live.RunTarget().Facts == nil || live.RunTarget().Facts.CachePolicy.Mode != backends.RouteCacheModes.CACHEDISABLED {
		t.Fatalf("target spec=%+v want=%+v facts=%+v saved=%+v", live.RunTarget().Spec, beforeSpec, live.RunTarget().Facts, saved)
	}
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireCacheView(t, ui, "Saved policy: default")
	requireCacheView(t, ui, "Active effective markers: disabled")
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyUp})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, err = s.Registry.GetRouteCachePolicy(t.Context(), spec.Name, spec.Model); !errors.Is(err, backends.ErrRouteCachePolicyNotFound) {
		t.Fatalf("clear: %v", err)
	}
	closeCacheEditor(t, ui)
	if live.RunTarget().Facts.CachePolicy.Mode != backends.RouteCacheModes.CACHEDISABLED {
		t.Fatalf("cleared factory facts = %+v", live.RunTarget().Facts.CachePolicy)
	}
}

func TestRouteCacheEditorOpaqueAliasDoesNotRepointActive(t *testing.T) {
	s, ui, live, _ := newCacheEditorUI(t)
	before := live.RunTarget().Provider
	openCacheEditor(t, ui, true)
	step(t, ui, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	alias := "  Team/Δ/Case\x1b[31m  "
	step(t, ui, tea.PasteMsg{Content: alias})
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireCacheView(t, ui, `\x1b[31m`)
	cacheChoice(t, ui, 2)
	saved, err := s.Registry.GetRouteCachePolicy(t.Context(), "proxy Δ", alias)
	if err != nil || saved.Mode != backends.RouteCacheModes.CACHEDISABLED {
		t.Fatalf("opaque saved = %+v, %v", saved, err)
	}
	closeCacheEditor(t, ui)
	if live.RunTarget().Provider != before {
		t.Fatal("unrelated alias refreshed active")
	}
}

var errCacheStorage = errors.New("cache storage unavailable")

type failingCacheStore struct {
	backends.RouteCachePolicyStore
	read, write bool
}

func (s failingCacheStore) GetRouteCachePolicy(ctx context.Context, p, a string) (backends.RouteCachePolicy, error) {
	if s.read {
		return backends.RouteCachePolicy{}, errCacheStorage
	}
	return s.RouteCachePolicyStore.GetRouteCachePolicy(ctx, p, a)
}
func (s failingCacheStore) SetRouteCachePolicy(ctx context.Context, p, a string, v backends.RouteCachePolicy) error {
	if s.write {
		return errCacheStorage
	}
	return s.RouteCachePolicyStore.SetRouteCachePolicy(ctx, p, a, v)
}
func TestRouteCacheEditorReadWriteFailures(t *testing.T) {
	for _, read := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "read"}[read], func(t *testing.T) {
			s, ui, live, spec := newCacheEditorUI(t)
			before := live.RunTarget().Provider
			s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(routeEditorDefinitions()), backends.WithRouteProtocolStore(s.Store), backends.WithRouteCachePolicyStore(failingCacheStore{s.Store, read, true}))
			openCacheEditor(t, ui, false)
			step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
			if read {
				requireCacheView(t, ui, "Saved policy unavailable:")
			}
			cacheChoice(t, ui, 1)
			requireCacheView(t, ui, "Policy not saved:")
			closeCacheEditor(t, ui)
			if live.RunTarget().Provider != before {
				t.Fatal("failure changed runtime")
			}
			if _, err := s.Store.GetRouteCachePolicy(t.Context(), spec.Name, spec.Model); !errors.Is(err, backends.ErrRouteCachePolicyNotFound) {
				t.Fatalf("unexpected persistence: %v", err)
			}
		})
	}
}
