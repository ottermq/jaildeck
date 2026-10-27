package freebsd

import (
	"os"

	"github.com/ottermq/jaildeck/internal/system"
)

type Adapter struct {
	runner          system.CommandRunner
	confFile        string
	confDir         string
	dmesgBoot       string
	uefiFirmware    string
	devfsRulesFiles []string
	// vmTemplateDir overrides <datastore>/.templates, and vmScratchDir is
	// where vm create's keys file is written for the moment it is read
	// (JD-038). Both are for tests; a host never changes them.
	vmTemplateDir string
	vmScratchDir  string
}

func NewAdapter(runner system.CommandRunner) *Adapter {
	return &Adapter{
		runner:          runner,
		confFile:        defaultJailConf,
		confDir:         defaultJailConfDir,
		dmesgBoot:       defaultDmesgBoot,
		uefiFirmware:    defaultUEFIFirmware,
		devfsRulesFiles: defaultDevfsRulesFiles,
		vmScratchDir:    os.TempDir(),
	}
}
