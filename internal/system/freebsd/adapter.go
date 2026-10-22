package freebsd

import "github.com/ottermq/jaildeck/internal/system"

type Adapter struct {
	runner          system.CommandRunner
	confFile        string
	confDir         string
	dmesgBoot       string
	devfsRulesFiles []string
}

func NewAdapter(runner system.CommandRunner) *Adapter {
	return &Adapter{
		runner:          runner,
		confFile:        defaultJailConf,
		confDir:         defaultJailConfDir,
		dmesgBoot:       defaultDmesgBoot,
		devfsRulesFiles: defaultDevfsRulesFiles,
	}
}
