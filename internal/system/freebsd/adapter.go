package freebsd

import "codeberg.org/Otter-Labs/jaildeck/internal/system"

type Adapter struct {
	runner system.CommandRunner
}

func NewAdapter(runner system.CommandRunner) *Adapter {
	return &Adapter{
		runner: runner,
	}
}
