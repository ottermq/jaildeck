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
	// hostRoot is the host's own tree, whose freebsd-version is its userland
	// (#229). For tests; a host never changes it.
	hostRoot string
	// vulnDatabase is the host's vulnerability database, and auditUser who
	// reads package databases and runs pkg audit (#230, JD-061). For tests;
	// a host never changes them.
	vulnDatabase string
	auditUser    string
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
		hostRoot:        "/",
		vulnDatabase:    defaultVulnDatabase,
		auditUser:       "nobody",
	}
}
