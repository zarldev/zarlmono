package transcript

import "fmt"

// InputAdmitted records a host result incorporated into a parent's model context.
// It does not claim the corresponding runner history is durably saved.
type InputAdmitted struct {
	TurnID    string
	Text      string
	Admission InputAdmission
}

// InputWaitChanged records a historical wait transition. Restoring this event
// never creates a live wait, task group or cancellation subscription.
type InputWaitChanged struct {
	TurnID  string
	Text    string
	Waiting bool
}

func (e InputAdmitted) validate() error {
	if e.TurnID == "" || e.Text == "" || e.Admission.Namespace == "" || e.Admission.ID == "" {
		return fmt.Errorf("%w: input admission identity or text is empty", ErrInvalidEvent)
	}
	return nil
}

func (e InputWaitChanged) validate() error {
	if e.TurnID == "" || e.Text == "" {
		return fmt.Errorf("%w: input wait turn or text is empty", ErrInvalidEvent)
	}
	return nil
}
