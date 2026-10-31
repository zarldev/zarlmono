package tui

//go:generate go tool goenums -f foreground_inspection_demand_enum.go

// foregroundInspectionKind identifies the one foreground action waiting for an
// advisory inspection worker to drain. Zero means no pending demand.
type foregroundInspectionKind int

const (
	foregroundInspectionSubmit foregroundInspectionKind = iota + 1 // submit
	foregroundInspectionDirect                                     // direct
	foregroundInspectionQueue                                      // queue
	foregroundInspectionSwarm                                      // swarm
	foregroundInspectionTarget                                     // target
)
