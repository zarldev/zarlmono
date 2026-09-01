package tui

//go:generate go tool goenums -f command_id_enum.go

// commandID is the goenums source for commandID. It identifies one user-facing
// command independently from its display label and shortcut.
type commandID int

const (
	commandHelp             commandID = iota // help
	commandSettings                          // settings
	commandTheme                             // theme
	commandModels                            // models
	commandNameSession                       // name-session
	commandPlan                              // plan
	commandToolHistory                       // tool-history
	commandFiles                             // files
	commandCopyLastResponse                  // copy-last-response
	commandExportSession                     // export-session
)
