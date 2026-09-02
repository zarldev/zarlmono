package transcript

//go:generate go tool goenums -f persistence_enum.go

// persistence is the goenums source for Persistence. It declares how an
// accepted semantic mutation should reach durable storage.
type persistence int

const (
	persistenceNone      persistence = iota // none
	persistenceDebounced                    // debounced
	persistenceImmediate                    // immediate
)
