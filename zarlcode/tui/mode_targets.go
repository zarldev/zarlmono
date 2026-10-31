package tui

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

var errModeTargetPending = errors.New("mode target is not ready")

type settingsTargetSnapshot struct {
	primary engine.ProviderSpec
	route   engine.ProviderSpec
	plan    bool
}

type modeTargetLifecycle struct {
	generation       uint64
	modeGeneration   uint64
	plan             bool
	sessionID        string
	manualOverride   bool
	deferred         bool
	building         bool
	prepared         *providerRepointedMsg
	err              error
	launchQueue      bool
	settingsSnapshot *settingsTargetSnapshot
}

func (m *UI) maybeModeTarget() tea.Cmd {
	if m.live == nil || m.settings == nil || m.exactResume || m.modeTarget.manualOverride {
		return nil
	}
	mode := m.live.AppliedMode()
	return m.requestModeTarget(mode.Plan, mode.Generation)
}

func (m *UI) captureSettingsTargetSnapshot() {
	if m.live == nil || m.settings == nil {
		return
	}
	ctx := m.appContext()
	fallback, _ := m.session.ProviderContext()
	primary := m.settings.ActiveProvider(ctx, fallback)
	mode := m.live.AppliedMode()
	m.modeTarget.settingsSnapshot = &settingsTargetSnapshot{
		primary: primary,
		route:   m.settings.MainModeTarget(ctx, mode.Plan, m.live.RunTarget().Spec),
		plan:    mode.Plan,
	}
}

func (m *UI) takeSettingsTargetSnapshot() *settingsTargetSnapshot {
	snapshot := m.modeTarget.settingsSnapshot
	m.modeTarget.settingsSnapshot = nil
	return snapshot
}

func (m *UI) requestModeTarget(plan bool, modeGeneration uint64) tea.Cmd {
	if m.live == nil || m.settings == nil {
		return nil
	}
	if m.isSwarmOpen() {
		m.session.SetErrorToast("stop the swarm goal before changing targets")
		return m.toastExpiryCmd()
	}
	m.modeTarget.manualOverride = false
	m.modeTarget.generation++
	generation := m.modeTarget.generation
	m.modeTarget.modeGeneration = modeGeneration
	m.modeTarget.plan = plan
	m.modeTarget.sessionID = m.session.ID
	m.modeTarget.err = nil
	m.modeTarget.prepared = nil
	m.modeTarget.building = false
	atomic.AddUint64(&m.repointSeq, 1) // invalidate an older manual/settings build

	if m.liveOperation != nil || m.sessionRetry != nil {
		m.modeTarget.deferred = true
		return nil
	}
	m.modeTarget.deferred = false
	active := m.live.RunTarget().Spec
	spec := m.settings.MainModeTarget(m.appContext(), plan, active)
	if spec == active {
		return m.finishModeTargetWithoutSwitch()
	}
	reasoning, defWindow := activeProviderPolicy(m.settings, spec.Name)
	m.modeTarget.building = true
	m.session.SetToast("preparing " + modeTargetLabel(plan) + " target " + providerModelLabel(spec.Name, spec.Model) + "…")
	return m.buildModeTargetCmd(generation, modeGeneration, m.session.ID, plan, spec, reasoning, defWindow)
}

func (m *UI) buildModeTargetCmd(
	generation, modeGeneration uint64,
	sessionID string,
	plan bool,
	spec engine.ProviderSpec,
	reasoning llm.ReasoningHistory,
	defWindow int,
) tea.Cmd {
	settings := m.settings
	parent := m.appContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, repointTimeout)
		defer cancel()
		prov, err := engine.BuildProvider(ctx, settings.Registry, settings.Svc, spec)
		if err != nil {
			return providerRepointedMsg{route: true, routeGeneration: generation, modeGeneration: modeGeneration, sessionID: sessionID, plan: plan, spec: spec, err: err}
		}
		window := settings.ContextWindow(ctx, spec)
		if window <= 0 {
			window = engine.LiveContextWindow
		}
		return providerRepointedMsg{
			route: true, routeGeneration: generation, modeGeneration: modeGeneration, sessionID: sessionID, plan: plan,
			prov: prov, spec: spec, window: window, reasoning: reasoning, defWindow: defWindow,
		}
	}
}

func (m *UI) handleModeTargetRepoint(rp providerRepointedMsg) {
	state := &m.modeTarget
	requestMatches := rp.routeGeneration == state.generation && rp.modeGeneration == state.modeGeneration && rp.plan == state.plan && rp.sessionID == state.sessionID
	if !requestMatches {
		return
	}
	mode := m.live.AppliedMode()
	if m.session.ID != rp.sessionID || mode.Generation != rp.modeGeneration || mode.Plan != rp.plan {
		*state = modeTargetLifecycle{generation: state.generation}
		return
	}
	state.building = false
	if rp.err != nil {
		state.err = fmt.Errorf("prepare %s target: %w", modeTargetLabel(rp.plan), rp.err)
		m.session.SetErrorToast(state.err.Error())
		return
	}
	if m.exactResume && !m.live.SupportsExactTarget(engine.TargetUpdate{Provider: rp.prov, Spec: rp.spec, Window: rp.window}) {
		state.err = errors.New("mode target cannot preserve this conversation's exact replay support; choose another target or start a new conversation")
		m.session.SetErrorToast(state.err.Error())
		return
	}
	state.prepared = &rp
	if err := m.applyPreparedModeTarget(nil); err != nil {
		if errors.Is(err, engine.ErrRuntimeBusy) {
			if state.launchQueue {
				m.scheduledModeTargetCmd = m.launchQueuedTurn()
			}
			return
		}
		state.err = fmt.Errorf("apply mode target: %w", err)
		m.session.SetErrorToast(state.err.Error())
	}
}

// applyPreparedModeTarget publishes before checkpoint capture under exclusive
// runtime admission; queued dispatch can transfer that same reservation.
func (m *UI) applyPreparedModeTarget(reservation *engine.RuntimeReservation) error {
	state := &m.modeTarget
	if state.prepared == nil {
		return nil
	}
	owned := false
	if reservation == nil {
		var err error
		reservation, err = m.live.ReserveRuntime()
		if err != nil {
			return err
		}
		owned = true
	}
	if owned {
		defer func() {
			if owned {
				reservation.Release()
			}
		}()
	}
	rp := *state.prepared
	if err := reservation.ApplyTarget(engine.TargetUpdate{Provider: rp.prov, Spec: rp.spec, Window: rp.window}); err != nil {
		return err
	}
	m.appliedReasoning = rp.reasoning
	m.appliedWindow = rp.defWindow
	m.session.SetActiveProviderSpec(rp.spec)
	m.session.ApplyProviderCostBasis(rp.spec)
	if rp.window > 0 {
		m.session.SetContextWindow(rp.window)
		m.SetPressureConfig(rp.window, m.session.Run.pressureReserve)
	}
	launchQueue := state.launchQueue && owned
	*state = modeTargetLifecycle{generation: state.generation, modeGeneration: state.modeGeneration, plan: state.plan, sessionID: state.sessionID}
	m.session.SetSuccessToast("using " + modeTargetLabel(rp.plan) + " target " + providerModelLabel(rp.spec.Name, rp.spec.Model))
	if owned {
		reservation.Release()
		owned = false
	}
	if launchQueue {
		m.scheduledModeTargetCmd = m.launchQueuedTurn()
	}
	return nil
}

func (m *UI) modeTargetReservation(prompt string, queuedID int) (*engine.RuntimeReservation, error) {
	state := &m.modeTarget
	if state.err != nil {
		return nil, state.err
	}
	if state.deferred || state.building {
		return nil, errModeTargetPending
	}
	if state.prepared == nil {
		if queuedID != 0 {
			return m.live.ReserveQueuedTurn(queuedID, prompt)
		}
		return m.live.ReserveTurn()
	}
	var (
		reservation *engine.RuntimeReservation
		err         error
	)
	if queuedID != 0 {
		reservation, err = m.live.ReserveQueuedTargetTurn(queuedID, prompt)
	} else {
		reservation, err = m.live.ReserveTargetTurn()
	}
	if err != nil {
		return nil, err
	}
	if queuedID != 0 {
		state.launchQueue = false
	}
	if err := m.applyPreparedModeTarget(reservation); err != nil {
		reservation.Release()
		return nil, err
	}
	return reservation, nil
}

// showModeTargetBlocked distinguishes normal preparation from a failed target.
// The admission checks still retain the draft or queued input in both cases.
func (m *UI) showModeTargetBlocked(err error, retained string) {
	if errors.Is(err, errModeTargetPending) {
		status := "preparing " + modeTargetLabel(m.modeTarget.plan) + " target"
		if m.modeTarget.deferred {
			status = "waiting for the current turn before switching targets"
		}
		m.session.SetToastTone(status+retained, toastInfo)
		return
	}
	m.session.SetErrorToast("mode target: " + err.Error() + retained)
}

func (m *UI) modeTargetSubmitBlocked() error {
	if m.modeTarget.err != nil {
		return m.modeTarget.err
	}
	if m.modeTarget.deferred || m.modeTarget.building {
		return errModeTargetPending
	}
	return nil
}

func (m *UI) deferModeTargetQueueLaunch(launch bool) tea.Cmd {
	if !m.modeTarget.deferred {
		return nil
	}
	m.modeTarget.launchQueue = launch
	return m.requestModeTarget(m.modeTarget.plan, m.modeTarget.modeGeneration)
}

func (m *UI) markManualTargetOverride() {
	snapshot := m.modeTarget.settingsSnapshot
	m.modeTarget.generation++
	m.modeTarget = modeTargetLifecycle{
		generation:       m.modeTarget.generation,
		manualOverride:   true,
		settingsSnapshot: snapshot,
	}
}

func (m *UI) finishModeTargetWithoutSwitch() tea.Cmd {
	launchQueue := m.modeTarget.launchQueue
	generation := m.modeTarget.generation
	m.modeTarget = modeTargetLifecycle{generation: generation, modeGeneration: m.modeTarget.modeGeneration, plan: m.modeTarget.plan, sessionID: m.modeTarget.sessionID}
	if launchQueue {
		return m.launchQueuedTurn()
	}
	return nil
}

func modeTargetLabel(plan bool) string {
	if plan {
		return "Plan"
	}
	return "Build"
}
