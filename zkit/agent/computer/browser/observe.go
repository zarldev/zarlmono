package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
	"github.com/zarldev/zarlmono/zkit/agent/computer"
)

// Observe returns the current browser surface state, including optional visible
// text, target descriptors, screenshot, and backend raw metadata requested by
// req.
func (s *Session) Observe(ctx context.Context, req computer.ObserveRequest) (computer.Observation, error) {
	var state pageState
	if err := s.run(ctx, chromedp.Evaluate(observeScript(req.IncludeTargets, req.IncludeRaw), &state)); err != nil {
		return computer.Observation{}, fmt.Errorf("observe browser surface: %w", err)
	}

	obs := computer.Observation{
		Surface: computer.SurfaceInfo{
			Kind:   computer.SurfaceKinds.BROWSER,
			Title:  state.Title,
			URL:    state.URL,
			Width:  state.Width,
			Height: state.Height,
		},
		FocusedTarget: convertTarget(state.FocusedTarget),
	}
	if req.IncludeText {
		obs.VisibleText = strings.TrimSpace(state.VisibleText)
	}
	if req.IncludeTargets {
		obs.Targets = convertTargets(state.Targets)
	}
	if req.IncludeRaw {
		obs.Raw = map[string]any{
			"ready_state": state.ReadyState,
		}
	}
	if req.IncludeScreenshot {
		var png []byte
		if err := s.run(ctx, chromedp.FullScreenshot(&png, 90)); err != nil {
			return computer.Observation{}, fmt.Errorf("capture browser screenshot: %w", err)
		}
		obs.Screenshot = &computer.ObservationImage{
			MIMEType: "image/png",
			DataURI:  "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		}
	}
	return obs, nil
}

type pageState struct {
	Title         string     `json:"title"`
	URL           string     `json:"url"`
	Width         int        `json:"width"`
	Height        int        `json:"height"`
	VisibleText   string     `json:"visible_text"`
	ReadyState    string     `json:"ready_state"`
	FocusedTarget *jsTarget  `json:"focused_target"`
	Targets       []jsTarget `json:"targets"`
}

type jsTarget struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	Name        string `json:"name"`
	Text        string `json:"text"`
	Description string `json:"description"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Enabled     bool   `json:"enabled"`
	Visible     bool   `json:"visible"`
	Focused     bool   `json:"focused"`
	Value       string `json:"value"`
}

func convertTargets(targets []jsTarget) []computer.TargetDescriptor {
	if len(targets) == 0 {
		return nil
	}
	out := make([]computer.TargetDescriptor, 0, len(targets))
	for i := range targets {
		out = append(out, convertTargetValue(targets[i]))
	}
	return out
}

func convertTarget(target *jsTarget) *computer.TargetDescriptor {
	if target == nil || target.ID == "" {
		return nil
	}
	converted := convertTargetValue(*target)
	return &converted
}

func convertTargetValue(target jsTarget) computer.TargetDescriptor {
	return computer.TargetDescriptor{
		ID:          target.ID,
		Role:        target.Role,
		Name:        target.Name,
		Text:        target.Text,
		Description: target.Description,
		Bounds: &computer.Rect{
			X:      target.X,
			Y:      target.Y,
			Width:  target.Width,
			Height: target.Height,
		},
		Enabled: target.Enabled,
		Visible: target.Visible,
		Focused: target.Focused,
		Value:   target.Value,
	}
}

func observeScript(includeTargets, includeRaw bool) string {
	return fmt.Sprintf(`(() => {
%s
const focused = describeTarget(document.activeElement, 0);
const targets = %t ? Array.from(document.querySelectorAll('a,button,input,textarea,select,[role],[aria-label],[contenteditable="true"]')).slice(0, 200).map((el, i) => describeTarget(el, i)).filter(Boolean) : [];
return {
  title: document.title || '',
  url: window.location.href || '',
  width: window.innerWidth || 0,
  height: window.innerHeight || 0,
  visible_text: document.body ? document.body.innerText || '' : '',
  ready_state: %t ? document.readyState || '' : '',
  focused_target: focused,
  targets: targets,
};
})()`, browserJSLibrary(), includeTargets, includeRaw)
}
