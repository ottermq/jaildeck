package common

import (
	"log/slog"
)

// NewComponentLogger returns a logger tagged with the component name, so every
// record says which part of Jail Deck produced it.
//
// Call it at the point of use. Never assign the result to a package-level var:
// slog.Default() is replaced in main after package initialisation, so a var
// captures the pre-configuration logger — which routes through the standard log
// package. Records then arrive at the wrong level with their attributes
// flattened into the message string, which still looks plausible in the output.
func NewComponentLogger(component string) *slog.Logger {
	return slog.Default().With("component", component)
}
