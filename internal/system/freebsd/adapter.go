package freebsd

import "codeberg.org/Otter-Labs/jaildeck/internal/system"

type Adapter struct {
	runner   system.CommandRunner
	confFile string
	confDir  string
}

func NewAdapter(runner system.CommandRunner) *Adapter {
	return &Adapter{
		runner:   runner,
		confFile: defaultJailConf,
		confDir:  defaultJailConfDir,
	}
}
