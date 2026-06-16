package main

import "sync"

// Status is the health of a single endpoint.
type Status string

// The endpoint health states the fake farm reports. Transient resolves
// itself after a few checks; Unknown means not yet probed.
const (
	StatusHealthy   Status = "healthy"
	StatusDegraded  Status = "degraded"
	StatusDown      Status = "down"
	StatusTransient Status = "transient"
	StatusUnknown   Status = "unknown" // not yet checked
)

// ServerFarm is the fake world: a set of named endpoints with mutable health.
// Endpoints start as "unknown" and must be checked before they count toward
// AllHealthy. The model drives check_endpoint to discover status; transient
// failures auto-resolve to healthy after one check (the farm auto-promotes
// on the first check of a transient endpoint, so a retry succeeds).
type ServerFarm struct {
	mu        sync.Mutex
	endpoints map[string]Status
	checked   []string
}

// NewServerFarm creates a farm with the given endpoints, all initially unknown.
func NewServerFarm(names ...string) *ServerFarm {
	eps := make(map[string]Status, len(names))
	for _, n := range names {
		eps[n] = StatusUnknown
	}
	return &ServerFarm{endpoints: eps}
}

// SetHealth sets an endpoint's status. Use before a run to simulate a
// degraded, down, or transient endpoint. The default is StatusUnknown.
func (f *ServerFarm) SetHealth(name string, s Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endpoints[name] = s
}

// Check returns the current status of name and logs the check. If the status
// is unknown, a successful check promotes it to healthy. If it is transient,
// the check auto-promotes to healthy (simulating a one-retry resolution) so
// the next call succeeds.
func (f *ServerFarm) Check(name string) Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.endpoints[name]
	if !ok {
		return StatusDown
	}
	f.checked = append(f.checked, name)
	switch s {
	case StatusUnknown:
		f.endpoints[name] = StatusHealthy
		return StatusHealthy
	case StatusTransient:
		f.endpoints[name] = StatusHealthy
		return StatusTransient // return transient so the model sees the failure
	default:
		return s
	}
}

// AllHealthy reports whether every known endpoint is healthy.
func (f *ServerFarm) AllHealthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.endpoints {
		if s != StatusHealthy {
			return false
		}
	}
	return true
}

// Snapshot returns a copy of the current endpoint→status map and check log.
func (f *ServerFarm) Snapshot() (map[string]Status, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	eps := make(map[string]Status, len(f.endpoints))
	for k, v := range f.endpoints {
		eps[k] = v
	}
	chk := make([]string, len(f.checked))
	copy(chk, f.checked)
	return eps, chk
}
