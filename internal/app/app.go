package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/firewall"
	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/images"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/metrics"
	"github.com/ottermq/jaildeck/internal/state"
	"github.com/ottermq/jaildeck/internal/storage"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/system/fake"
	"github.com/ottermq/jaildeck/internal/system/freebsd"
	"github.com/ottermq/jaildeck/internal/templates"
	"github.com/ottermq/jaildeck/internal/vms"
)

var ErrNoCredential = errors.New("no operator credential configured")

type App struct {
	authHandler     *auth.AuthHandler
	jailHandler     *jails.JailHandler
	hostHandler     *host.HostHandler
	auditHandler    *audit.AuditHandler
	storageHandler  *storage.StorageHandler
	templateHandler *templates.TemplateHandler
	metricsHandler  *metrics.Handler
	firewallHandler *firewall.Handler
	vmHandler       *vms.Handler
	imageHandler    *images.Handler
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
		Exec:          jailAdapter.exec_,
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

	// The host half and the per-jail half come from the same FreeBSD
	// adapter; the jail listing comes from the jails service, which
	// satisfies the port without importing it (JD-010, JD-031).
	hostMetrics, disks, err := newHostMetrics(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	metricsHandler := metrics.NewHandler(
		metrics.NewService(hostMetrics, jailAdapter.usage, jailService, disks))

	firewallSystem, err := newFirewallSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	firewallHandler := firewall.NewHandler(firewall.NewService(firewallSystem))

	imageSystem, err := newImageSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	imageService := images.NewService(imageSystem, auditLogger)
	imageHandler := images.NewHandler(imageService)

	vmSystem, err := newVMSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	vmHandler := vms.NewHandler(vms.NewService(vmSystem, vmImageCatalog{images: imageService}, auditLogger))

	return &App{
		authHandler:     authHandler,
		jailHandler:     jailHandler,
		hostHandler:     hostHandler,
		auditHandler:    auditHandler,
		storageHandler:  storageHandler,
		templateHandler: templateHandler,
		metricsHandler:  metricsHandler,
		firewallHandler: firewallHandler,
		vmHandler:       vmHandler,
		imageHandler:    imageHandler,
	}, nil
}

// newFirewallSystem selects the read-only PF implementation (JD-034).
func newFirewallSystem(adapter string) (firewall.FirewallSystem, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewFirewall(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newVMSystem selects the vm-bhyve implementation (JD-035, JD-036, JD-038).
func newVMSystem(adapter string) (vms.VMSystem, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewVMs(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newImageSystem selects the VM image implementation (JD-037).
func newImageSystem(adapter string) (images.ImageSystem, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewImageAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewImages(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newHostMetrics selects the host-metrics and disk implementations, with the
// same refusal to guess as every other adapter choice here. One object
// satisfies both ports: the host's numbers and its disks come from the same
// machine.
func newHostMetrics(adapter string) (metrics.HostMetrics, metrics.DiskSource, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		a := freebsd.NewAdapter(system.NewExecCommandRunner())
		return a, a, nil
	case config.AdapterFake:
		f := fake.NewHostMetrics()
		return f, f, nil
	default:
		return nil, nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
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
	usage           metrics.JailUsageSource
	exec_           jails.JailExecutor
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
			configReader: a, autostartSource: a, usage: a, exec_: a}, nil
	case config.AdapterFake:
		f := fake.NewJailSystem()
		return jailAdapters{system: f, config: f, remover: f, autostart: f,
			bridges: f, epairs: f, epair: f, rctl: f, devfsRulesets: f,
			configReader: f, autostartSource: f, usage: f, exec_: f}, nil
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
