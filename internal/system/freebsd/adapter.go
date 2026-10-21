package freebsd

import "github.com/ottermq/jaildeck/internal/system"

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
