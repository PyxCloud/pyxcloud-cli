package passoauth

// PhaseError records only a closed execution phase, never native messages or
// request data. Unwrap preserves cancellation and deadline classification.
type PhaseError struct {
	Phase string
	Cause error
}

func (e *PhaseError) Error() string { return e.Phase }
func (e *PhaseError) Unwrap() error { return e.Cause }
func ContextPhase(phase string, cause error) error {
	switch phase {
	case "credential_read", "credential_write", "refresh", "http_request":
		return &PhaseError{Phase: phase, Cause: cause}
	default:
		return cause
	}
}
