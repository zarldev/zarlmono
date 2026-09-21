package teasink

import tea "charm.land/bubbletea/v2"

// AfterEvents enqueues an application-owned marker after all events published so
// far, including coalesced content. Unlike Drain's pump-only acknowledgment, the
// marker reaches the application's Update loop: handling it there establishes
// that preceding events were applied, not merely delivered to Program.Send.
//
// The caller must first settle the event producers and bind the marker to its
// session, operation generation and turn. This method neither establishes runtime
// quiescence nor waits for application. Call it off the Update loop because a
// full pump queue applies backpressure. An unwired or closed sink drops markers
// just like events; callers must not interpret command completion as application.
func (s *Sink) AfterEvents(marker tea.Msg) {
	s.flush()
	s.dispatch(marker)
}
