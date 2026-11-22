package tui

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

// settleSessionShutdown owns event application, runtime joins and the existing
// persistence FIFO after Update stops. No shutdown goroutine or replacement
// runtime is created. State retained on UI makes context timeout resumable.
func (m *UI) settleSessionShutdown(ctx context.Context) error {
	m.stopShutdownProducers()
	if err := ctx.Err(); err != nil {
		return err
	}
	receipts := false
	proofUnavailable := false
	finalDraftCaptured := false
	if m.liveSink != nil {
		err := m.liveSink.TakeApplications(ctx)
		switch {
		case err == nil:
			receipts = true
		case errors.Is(err, teasink.ErrApplicationsUnavailable):
			proofUnavailable = true
		default:
			return err
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(m.shutdownError, err)
		}
		m.retainShutdownJoins()
		var changed <-chan struct{}
		if receipts {
			if err := m.liveSink.TakeApplications(ctx); err != nil {
				return errors.Join(m.shutdownError, err)
			}
			delivery, available, wake := m.liveSink.NextApplication()
			changed = wake
			if available {
				var applicationErr error
				if !delivery.Apply(func(payload tea.Msg) {
					_, handled, err := m.applyLiveEvent(payload)
					if !handled || err != nil {
						applicationErr = errLiveApplicationUnknown
					}
				}) {
					return errors.Join(m.shutdownError, teasink.ErrApplicationDeliveryLost)
				}
				if applicationErr != nil {
					m.applicationError = applicationErr
				}
				if m.unsavedTurnError != nil {
					m.shutdownError = errors.Join(m.shutdownError, m.unsavedTurnError)
				}
				continue
			}
		}
		if m.applicationError != nil {
			return errors.Join(m.shutdownError, m.applicationError)
		}
		if progressed, err := m.joinShutdownPersistence(proofUnavailable); err != nil {
			return err
		} else if progressed {
			continue
		}
		if progressed, err := m.manualShutdown(); err != nil {
			return errors.Join(m.shutdownError, err)
		} else if progressed {
			continue
		}
		if runtime := m.shutdownRuntime; runtime != nil && !runtime.runtimeJoined {
			select {
			case _, ok := <-runtime.runtimeDone:
				if !ok {
					return errors.Join(m.shutdownError, errLiveApplicationUnknown)
				}
				runtime.runtimeJoined = true
			default:
			}
		}
		if progressed, err := m.persistShutdownUnstarted(ctx); err != nil {
			return err
		} else if progressed {
			continue
		}
		// A recorded callback waits for its full-save disposition before its
		// worker can join. Advance the persistence FIFO before waiting for that
		// worker, while still applying any newly available producer receipts.
		if m.sessionPersistCurrent == nil && len(m.sessionPersistQueue) != 0 {
			op := m.sessionPersistQueue[0]
			m.sessionPersistQueue = m.sessionPersistQueue[1:]
			if op.sessionID() == m.transcriptPersistedSessionID {
				op.rebaseTranscript(m.transcriptPersisted)
			}
			op.claimed = new(atomic.Bool)
			m.sessionPersistCurrent = &op
			m.sessionPersistRunning = true
			continue
		}
		if waited, err := m.waitShutdownJoins(ctx, changed); err != nil {
			return err
		} else if waited {
			continue
		}
		if m.manualCompaction != nil {
			return errors.Join(m.shutdownError, errLiveApplicationUnknown)
		}
		if m.sessionPersistCurrent != nil {
			return errors.Join(m.shutdownError, errLiveApplicationUnknown)
		}
		if proofUnavailable && m.liveOperation != nil && !m.liveOperation.completed {
			return errors.Join(m.shutdownError, teasink.ErrApplicationsUnavailable)
		}
		if m.liveOperation != nil {
			return errors.Join(m.shutdownError, fmt.Errorf("shutdown turn settlement: %w", errLiveApplicationUnknown))
		}
		if m.shutdownProtected || m.shutdownDeleting {
			return m.shutdownError
		}
		if !finalDraftCaptured && !m.sourceConflict && m.rewindRecovery == "" && m.settings != nil && m.settings.Store != nil && m.intro == nil &&
			(m.composer.text() != "" || len(m.pendingAttachments) != 0 || len(m.rejectedDraftJSON) == 0) {
			finalDraftCaptured = true
			if _, err := m.sessionSnapshot(); errors.Is(err, errSessionSnapshotEmpty) {
				m.enqueueDraftPersist(m.draftGeneration)
				if len(m.sessionPersistQueue) != 0 {
					continue
				}
			}
		}
		// Final composer/context save uses the normal guarded source receipt. The
		// preceding settled full save is never skipped on transcript-revision equality.
		return errors.Join(m.shutdownError, m.SaveSession(ctx))
	}
}

// stopShutdownProducers fences admission before taking the live event consumer.
func (m *UI) stopShutdownProducers() {
	m.shutdownDeleting = m.shutdownDeleting || m.sessionDeletionPending()
	if op := m.manualCompaction; op != nil {
		op.cancel()
	}
	for _, op := range m.sessionPersistQueue {
		if op.before != nil {
			op.before.stop()
		}
	}
	if current := m.sessionPersistCurrent; current != nil && current.cancel != nil {
		current.cancel()
	}
	if m.live != nil {
		m.live.CancelTurn()
	}
	if owner := m.foreground; owner != nil {
		m.shutdownForeground = owner
		owner.stopping = true
		owner.cancel()
	}
}

// retainShutdownJoins keeps command identity even when application clears it.
func (m *UI) retainShutdownJoins() {
	// Retain join identity before application can clear or replace the current
	// operation. done proves command return; its in-band marker proves order.
	if current := m.sessionPersistCurrent; current != nil && current.claimed.Load() && !current.shutdownOwned && !current.joined {
		m.shutdownJoin = current
	}
	if op := m.liveOperation; op != nil && op.runtimeDone != nil && m.shutdownRuntime == nil {
		m.shutdownRuntime = op
		if op.runtimeClaimed.CompareAndSwap(false, true) {
			// It was never invoked. Retain submitted input without inventing a turn.
			op.runtimeJoined = true
			if op.runtimeReservation != nil {
				op.runtimeReservation.Release()
			}
			if op.queuedID == 0 {
				m.restoreBeforeInput(beforeTurnFailedMsg{prompt: op.runtimePrompt, attachments: op.runtimeAttachments, err: context.Canceled})
			}
			m.liveOperation = nil
		}
	}
}

// joinShutdownPersistence distinguishes actual return from the in-band proof.
func (m *UI) joinShutdownPersistence(proofUnavailable bool) (bool, error) {
	if join := m.shutdownJoin; join != nil && !join.joined {
		select {
		case result, ok := <-join.done:
			if !ok {
				return false, errors.Join(m.shutdownError, errLiveApplicationUnknown)
			}
			join.joined, join.joinResult = true, result
			// Manual retry errors stay with their operation until storage settles.
			if result.err != nil && join.manual == nil {
				m.shutdownError = errors.Join(m.shutdownError, result.err)
			}
		default:
		}
	}
	if join := m.shutdownJoin; join != nil && join.joined {
		if join.before == nil && m.sessionPersistCurrent == join {
			m.handleDraftPersistenceMsg(join.joinResult)
		}
		if join.before != nil && proofUnavailable && m.sessionPersistCurrent == join {
			join.before.mu.Lock()
			admitted := join.before.admitted
			join.before.mu.Unlock()
			if admitted {
				return false, errors.Join(m.shutdownError, teasink.ErrApplicationsUnavailable)
			}
			// The actual executor never admitted a runtime. No runner events or
			// terminal boundary exist to prove; preserve its recovery disposition.
			m.shutdownProtected = true
			result := join.joinResult
			result.beforeGeneration = 0
			if op := m.liveOperation; op != nil && op.generation == join.beforeGeneration {
				m.resolveSwarmSettle(op, errSwarmShutdown)
				m.liveOperation = nil
			}
			m.handleDraftPersistenceMsg(result)
		}
		// BEFORE's done is never substituted for its application marker.
		if m.sessionPersistCurrent != join {
			m.shutdownJoin = nil
			return true, nil
		}
	}
	return false, nil
}

// persistShutdownUnstarted claims only recovery persistence, never execution.
func (m *UI) persistShutdownUnstarted(ctx context.Context) (bool, error) {
	if current := m.sessionPersistCurrent; current != nil && (current.shutdownOwned || current.claimed.CompareAndSwap(false, true)) {
		current.shutdownOwned = true
		err := m.flushUnstartedSessionPersist(ctx, current)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, errors.Join(m.shutdownError, err)
		}
		result := sessionPersistedMsg{kind: current.kind, generation: current.generation, sessionID: current.sessionID(), revision: current.transcriptRevision(), err: err, settledGeneration: current.settledGeneration, operation: current.done}
		current.done <- result
		close(current.done)
		current.joined = true
		if current.before != nil {
			// Recovery-only disposition: no Run, no synthetic runtime marker.
			m.shutdownProtected = true
			if op := m.liveOperation; op != nil && op.generation == current.beforeGeneration {
				m.resolveSwarmSettle(op, errSwarmShutdown)
				m.liveOperation = nil
			}
		}
		m.handleDraftPersistenceMsg(result)
		if err != nil && current.manual == nil {
			m.shutdownError = errors.Join(m.shutdownError, err)
		}
		return true, nil
	}
	return false, nil
}

// waitShutdownJoins waits only while every producer can still make progress:
// receipt availability wakes the caller before blocked producers are joined.
func (m *UI) waitShutdownJoins(ctx context.Context, changed <-chan struct{}) (bool, error) {
	// Commands constructed before shutdown may still be running. Drain while
	// joining them: a full event FIFO must not deadlock its producer's return.
	var persistenceDone <-chan sessionPersistedMsg
	if join := m.shutdownJoin; join != nil && !join.joined {
		persistenceDone = join.done
	}
	var runtimeDone <-chan tea.Msg
	if runtime := m.shutdownRuntime; runtime != nil && !runtime.runtimeJoined {
		runtimeDone = runtime.runtimeDone
	}
	var swarmDone <-chan struct{}
	if owner := m.shutdownForeground; owner != nil {
		done := owner.done
		if owner.closingAsync {
			done = owner.stopped
		}
		select {
		case <-done:
			if err := owner.goal.Close(ctx); err != nil {
				return false, errors.Join(m.shutdownError, err)
			}
			m.shutdownForeground = nil
			m.foreground = nil
		default:
			swarmDone = done
		}
	}
	manualDone := m.manualWaitChannel()
	if persistenceDone != nil || runtimeDone != nil || manualDone != nil || swarmDone != nil || (m.sessionPersistCurrent != nil && m.sessionPersistCurrent.before != nil) {
		select {
		case <-ctx.Done():
			return false, errors.Join(m.shutdownError, ctx.Err())
		case <-changed:
		case result, ok := <-persistenceDone:
			if !ok {
				return false, errors.Join(m.shutdownError, errLiveApplicationUnknown)
			}
			m.shutdownJoin.joined, m.shutdownJoin.joinResult = true, result
			if result.err != nil && m.shutdownJoin.manual == nil {
				m.shutdownError = errors.Join(m.shutdownError, result.err)
			}
		case _, ok := <-runtimeDone:
			if !ok {
				return false, errors.Join(m.shutdownError, errLiveApplicationUnknown)
			}
			m.shutdownRuntime.runtimeJoined = true
		case <-manualDone:
			if op := m.manualCompaction; op != nil {
				op.joined = true
			}
		case <-swarmDone:
		}
		return true, nil
	}
	return false, nil
}
