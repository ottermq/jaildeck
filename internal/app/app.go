package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/state"
	"github.com/ottermq/jaildeck/internal/storage"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/system/fake"
	"github.com/ottermq/jaildeck/internal/system/freebsd"
	"github.com/ottermq/jaildeck/internal/templates"
)

var ErrNoCredential = errors.New("no operator credential configured")

type App struct {
	authHandler     *auth.AuthHandler
	jailHandler     *jails.JailHandler
	hostHandler     *host.HostHandler
	auditHandler    *audit.AuditHandler
	storageHandler  *storage.StorageHandler
	templateHandler *templates.TemplateHandler
}

func New(cfg *config.Config) (*App, error) {
	authState := state.NewStore(cfg.StateDir)
	if err := requireCredential(authState); err != nil {
		return nil, err
	}

	sessions := auth.NewSessionStore()
	authService := auth.NewAuthService(authState, sessions)
	authHandler := auth.NewAuthHandler(authService)

	auditLogger, err := setupAuditLog(cfg.AuditLog)
	if err != nil {
		return nil, err
	}

	jailAdapter, err := newJailAdapters(cfg.Adapter)
	if err != nil {
		return nil, err
	}

	storageSystem, err := newStorageSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	storageService := storage.NewService(storageSystem, auditLogger, cfg.ZFSPool)

	storageHandler := storage.NewStorageHandler(storageService)

	packageSystem, err := newPackageSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	releaseSource, err := newReleaseSource(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	templateService := templates.NewService(
		storage.NewTemplateSource(storageService), packageSystem, releaseSource, auditLogger)
	templateHandler := templates.NewTemplateHandler(templateService)

	jailStorage := storage.NewJailSource(storageService)
	jailService := jails.NewJailService(jails.Dependencies{
		System:        jailAdapter.system,
		Audit:         auditLogger,
		Datasets:      jailStorage,
		DatasetWriter: jailStorage,
		Snapshots:     jailStorage,
		DatasetMover:  jailStorage,
		Recloner:      jailStorage,
		TemplateMaker: jailStorage,
		RootCloner:    jailStorage,
		Config:        jailAdapter.config,
		Autostart:     jailAdapter.autostart,
		RootDestroyer: jailStorage,
		ConfigRemover: jailAdapter.remover,
		Bridges:       jailAdapter.bridges,
		Epairs:        jailAdapter.epairs,
		Epair:         jailAdapter.epair,
		Rctl:          jailAdapter.rctl,
		DevfsRulesets: jailAdapter.devfsRulesets,

		ConfigReader:    jailAdapter.configReader,
		AutostartSource: jailAdapter.autostartSource,
		CloneOrigin:     jailStorage,
	})
	jailHandler := jails.NewJailHandler(jailService)

	hostSystem, err := newHostSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}

	hostService := host.NewService(hostSystem, auditLogger, cfg.ZFSPool)
	hostHandler := host.NewHostHandler(hostService)

	auditService := audit.NewAuditService(auditLogger)
	auditHandler := audit.NewAuditHandler(auditService)

	return &App{
		authHandler:     authHandler,
		jailHandler:     jailHandler,
		hostHandler:     hostHandler,
		auditHandler:    auditHandler,
		storageHandler:  storageHandler,
		templateHandler: templateHandler,
	}, nil
}

func requireCredential(store *state.Store) error {
	st, err := store.Load()
	switch {
	case errors.Is(err, state.ErrNotConfigured):
		return fmt.Errorf("%w in %s. Run: jaildeck passwd", ErrNoCredential, store.Dir())
	case err != nil:
		// corrupted or unreadable:
		// No need of dedicated sentinels
		// Load() already names the path and the cause, and this must not be reported as "run jaildeck passwd"
		return err
	case st.PasswordHash == "":
		return fmt.Errorf("%w in %s. Run: jaildeck passwd", ErrNoCredential, store.Dir())
	}
	return nil
}

func setupAuditLog(auditLogFilePath string) (*audit.FileLogger, error) {
	err := os.MkdirAll(filepath.Dir(auditLogFilePath), 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating audit log directory: %w", err)
	}
	f, err := os.OpenFile(auditLogFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening audit log: %w", err)
	}
	defer f.Close()

	auditLogger := audit.NewFileLogger(auditLogFilePath)
	return auditLogger, nil
}

// jailAdapters are the jail ports one adapter object implements: the jail
// system itself, the configuration file, rc.conf's jail_list, the host's
// bridges and epairs, and the kernel's rctl and devfs rulesets. They are
// built together because in fake mode they have to be the same object -- a
// jail whose stanza was just written must appear in the next listing, and
// one that was just started must carry the rules its stanza asked for.
type jailAdapters struct {
	system          jails.JailSystem
	config          jails.ConfigWriter
	remover         jails.ConfigRemover
	autostart       jails.Autostarter
	bridges         jails.BridgeSource
	epairs          jails.EpairAllocator
	epair           jails.EpairCleaner
	rctl            jails.RctlSystem
	devfsRulesets   jails.DevfsRulesetSource
	configReader    jails.ConfigReader
	autostartSource jails.AutostartSource
}

// newJailAdapters selects the implementations named by adapter. An unknown
// name is an error rather than a silent fallback: which adapter is running
// determines whether real jails are touched.
func newJailAdapters(adapter string) (jailAdapters, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		a := freebsd.NewAdapter(system.NewExecCommandRunner())
		return jailAdapters{system: a, config: a, remover: a, autostart: a,
			bridges: a, epairs: a, epair: a, rctl: a, devfsRulesets: a,
			configReader: a, autostartSource: a}, nil
	case config.AdapterFake:
		f := fake.NewJailSystem()
		return jailAdapters{system: f, config: f, remover: f, autostart: f,
			bridges: f, epairs: f, epair: f, rctl: f, devfsRulesets: f,
			configReader: f, autostartSource: f}, nil
	default:
		return jailAdapters{}, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newStorageSystem selects the storage.StorageSystem implementation named by
// adapter, with the same refusal to guess as newJailSystem.
func newStorageSystem(adapter string) (storage.StorageSystem, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewStorageAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewStorageSystem(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newPackageSystem selects the templates.PackageSystem implementation named by
// adapter, with the same refusal to guess as newJailSystem.
func newPackageSystem(adapter string) (templates.PackageSystem, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewPackageSystem(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newReleaseSource selects the templates.ReleaseSource implementation named by
// adapter, with the same refusal to guess as newJailSystem.
func newReleaseSource(adapter string) (templates.ReleaseSource, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewReleaseAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewReleaseSource(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newHostSystem selects the host.HostSystem implementation named by adapter,
// with the same refusal to guess as newJailSystem.
func newHostSystem(adapter string) (host.HostSystem, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewHostSystem(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}
