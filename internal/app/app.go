package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ottermq/jaildeck/internal/about"
	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/console"
	"github.com/ottermq/jaildeck/internal/events"
	"github.com/ottermq/jaildeck/internal/firewall"
	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/housekeeping"
	"github.com/ottermq/jaildeck/internal/images"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/metrics"
	"github.com/ottermq/jaildeck/internal/names"
	"github.com/ottermq/jaildeck/internal/networks"
	"github.com/ottermq/jaildeck/internal/osversion"
	"github.com/ottermq/jaildeck/internal/replication"
	"github.com/ottermq/jaildeck/internal/state"
	"github.com/ottermq/jaildeck/internal/storage"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/system/fake"
	"github.com/ottermq/jaildeck/internal/system/freebsd"
	"github.com/ottermq/jaildeck/internal/templates"
	"github.com/ottermq/jaildeck/internal/vms"
	"github.com/ottermq/jaildeck/internal/vulns"
)

var ErrNoCredential = errors.New("no operator credential configured")

// Back is the root side of Jail Deck (JD-056): the services, the adapters
// and the API's handlers, behind the principal the front names. It never
// reads the state directory: the front authenticates.
type Back struct {
	authHandler     *auth.AuthHandler
	housekeeping    *housekeeping.Handler
	eventHandler    *events.Handler
	jailHandler     *jails.JailHandler
	jailProperties  auth.JailProperties
	consoleHandler  *console.Handler
	nameHandler     *names.Handler
	aboutHandler    *about.Handler
	hostHandler     *host.HostHandler
	auditHandler    *audit.AuditHandler
	storageHandler  *storage.StorageHandler
	templateHandler *templates.TemplateHandler
	metricsHandler  *metrics.Handler
	firewallHandler *firewall.Handler
	vmHandler       *vms.Handler
	imageHandler    *images.Handler
	networkHandler  *networks.Handler
	vulnHandler     *vulns.Handler
	// vulnerabilities audits every package on a timer of its own, started
	// by Background (#230).
	vulnerabilities    *vulns.Service
	replicationHandler *replication.Handler
	// replication reads zrepl's cursors on a timer of its own, started by
	// Background (#247).
	replication *replication.Service
	// processes is Jail Deck's own memory, told the front's pid by SetFront
	// (#224).
	processes *ownProcesses
}

// SetFront tells the root side how to find the front's pid, 0 while it is
// not running, so /metrics can report the front's memory too (#224). Only
// a split Jail Deck has a front of its own.
func (a *Back) SetFront(pid func() int) { a.processes.front.Store(&pid) }

// Background starts what the root side does on its own rather than for a
// request: today, the package audit (#230). Separate from NewBack, so
// building a Back -- in a test -- starts nothing. It returns at once, and
// the work stops when ctx does.
func (a *Back) Background(ctx context.Context) {
	go a.vulnerabilities.Run(ctx, vulns.Poll, vulns.Every)
	go a.replication.Run(ctx, replication.Every)
}

// NewBack builds the root side. recheck answers whether a request's login
// still holds, for a console or an event stream that outlives its request:
// the front's to answer, since the sessions and the tokens are its.
func NewBack(cfg *config.Config, recheck func(*http.Request) error) (*Back, error) {
	// Only the principal the front names, never a credential: Trusted,
	// Restrict and Allow need no service.
	authHandler := auth.NewAuthHandler(nil)
	if cfg.NoConsoleUI {
		authHandler.SetSessionWithout(auth.ScopeConsole)
	}

	auditLogger, err := setupAuditLog(cfg.AuditLog)
	if err != nil {
		return nil, err
	}
	// Every service audits through the stream, which announces an entry
	// once it is written (#124, JD-052). The audit endpoint reads the file.
	broker := events.NewBroker()
	audited := liveAudit{next: auditLogger, broker: broker}

	jailAdapter, err := newJailAdapters(cfg.Adapter)
	if err != nil {
		return nil, err
	}

	storageSystem, err := newStorageSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	storageService := storage.NewService(storageSystem, audited, cfg.ZFSPool)

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
		storage.NewTemplateSource(storageService), packageSystem, releaseSource, audited)
	templateHandler := templates.NewTemplateHandler(templateService)

	jailStorage := storage.NewJailSource(storageService)
	jailService := jails.NewJailService(jails.Dependencies{
		System:        jailAdapter.system,
		Audit:         audited,
		Datasets:      jailStorage,
		DatasetWriter: jailStorage,
		Snapshots:     jailStorage,
		DatasetMover:  jailStorage,
		Children:      jailStorage,
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
		Limits:          jailAdapter.limits,
		ConfigReplacer:  jailAdapter.configReplacer,
	})
	jailHandler := jails.NewJailHandler(jailService)

	hostSystem, err := newHostSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}

	hostService := host.NewService(hostSystem, audited, cfg.ZFSPool)
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
	metricsService := metrics.NewService(hostMetrics, jailAdapter.usage, jailService, disks)
	metricsHandler := metrics.NewHandler(metricsService)

	// FreeBSD versions: each jail's and template's userland against the
	// host's, on their own pages and on the dashboard (#229, JD-060).
	versionSource, err := newVersionSource(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	jailService.SetVersions(versionSource)
	// Optional, like the other ports set after construction: a system that
	// cannot say whether a jail is gone leaves a re-clone without the wait.
	if removal, ok := jailAdapter.system.(jails.JailRemoval); ok {
		jailService.SetRemoval(removal)
	}
	templateService.SetVersions(versionSource)
	metricsService.SetVersions(metricsVersions{source: versionSource, jails: jailService, templates: templateService})

	// Vulnerable packages: the host's, each jail's and each template's,
	// audited against the host's database as nobody (#230, JD-061).
	vulnSource, err := newVulnSource(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	vulnService := vulns.NewService(vulnSource, vulnTargets{jails: jailService, templates: templateService})
	jailService.SetVulnerabilities(vulnService)
	templateService.SetVulnerabilities(vulnService)
	metricsService.SetVulnerabilities(vulnService)

	// ZFS replication: zrepl's cursors, read every minute (#247).
	replicationSource, err := newReplicationSource(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	replicationService := replication.NewService(replicationSource)
	metricsService.SetReplication(replicationService)

	// Jail Deck's own memory, on /metrics only (#224).
	residents, err := newResidentReader(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	processes := &ownProcesses{rss: residents}
	metricsService.SetProcesses(processes)

	firewallSystem, err := newFirewallSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	firewallHandler := firewall.NewHandler(firewall.NewService(firewallSystem))

	imageSystem, err := newImageSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	imageService := images.NewService(imageSystem, audited)
	imageHandler := images.NewHandler(imageService)

	networkSystem, err := NewNetworkSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	networkService := networks.NewService(networkSystem, audited, NetworkSettings(cfg))
	// The record of tenant networks follows the host, and a network a reboot
	// took is reported (JD-059).
	networkService.CheckRecord(context.Background())
	networkHandler := networks.NewHandler(networkService)
	jailService.SetNetworks(jailNetworks{networks: networkService})

	vmSystem, err := newVMSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	vmService := vms.NewService(vmSystem, vmImageCatalog{images: imageService}, audited)
	if cpu, ok := hostMetrics.(processCPU); ok {
		metricsService.SetGuests(metricsGuests{vms: vmService, cpu: cpu})
	}
	vmService.SetProperties(storageService)
	vmService.SetSnapshots(storage.NewGuestSnapshots(storageService))
	vmService.SetNetworks(guestNetworks{networks: networkService})
	if files, ok := vmSystem.(vms.GuestFiles); ok {
		vmService.SetFiles(files)
	}
	vmHandler := vms.NewHandler(vmService)
	// On a real host the switches are one thing; the two fakes are joined so
	// fake mode behaves the same.
	if n, ok := networkSystem.(*fake.Networks); ok {
		if v, ok := vmSystem.(*fake.VMs); ok {
			v.SwitchesFrom(n)
			n.GuestsFrom(v.GuestsOn)
			n.AddressesFrom(v.AddressesIn)
		}
		if j, ok := jailAdapter.system.(*fake.JailSystem); ok {
			n.AddressesFrom(j.AddressesIn)
			j.BridgesFrom(n.BridgeNames)
		}
	}

	// The console asks the jails service whether a jail is running, and the
	// jails service hangs a jail's consoles up before it stops (JD-042).
	shells, err := newShells(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	consoleService := console.NewService(shells, runningJails{jails: jailService},
		guestConsoles{vms: vmService}, audited)
	jailService.SetConsoles(consoleService)
	vmService.SetConsoles(consoleService)
	consoleHandler := console.NewHandler(consoleService, recheck, auth.PrincipalOf,
		func(r *http.Request) error { return authHandler.Allow(r, auth.ScopeConsole) },
		cfg.ConsoleIdle, cfg.ConsoleOrigins)

	nameHandler := names.NewHandler(names.NewSuggester(
		namesInUse{jails: jailService, vms: vmService, templates: templateService}))

	archives, err := newArchiveSource(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	housekeepingHandler := housekeeping.NewHandler(housekeeping.NewService(housekeeping.Sources{
		Templates: housekeepingTemplates{templates: templateService},
		Images:    housekeepingImages{images: imageService},
		Retained:  housekeepingRetained{storage: storageService},
		Snapshots: housekeepingSnapshots{jails: jailService},
		Archives:  archives,
	}))

	templateService.OnProgress(func(name, step string) {
		broker.Publish(events.TypeProgress, events.Progress{Kind: "template", Name: name, Step: step})
	})
	imageService.OnProgress(func(name, step string) {
		broker.Publish(events.TypeProgress, events.Progress{Kind: "image", Name: name, Step: step})
	})
	events.NewObserver(broker, liveInstances{jails: jailService, vms: vmService}, events.Interval)
	eventHandler := events.NewHandler(broker, recheck)

	return &Back{
		jailProperties:     jailProperties{jails: jailService}.Get,
		authHandler:        authHandler,
		housekeeping:       housekeepingHandler,
		eventHandler:       eventHandler,
		consoleHandler:     consoleHandler,
		nameHandler:        nameHandler,
		aboutHandler:       about.NewHandler(time.Now(), cfg.StateDir),
		jailHandler:        jailHandler,
		hostHandler:        hostHandler,
		auditHandler:       auditHandler,
		storageHandler:     storageHandler,
		templateHandler:    templateHandler,
		metricsHandler:     metricsHandler,
		firewallHandler:    firewallHandler,
		vmHandler:          vmHandler,
		imageHandler:       imageHandler,
		networkHandler:     networkHandler,
		vulnHandler:        vulns.NewHandler(vulnService),
		vulnerabilities:    vulnService,
		replicationHandler: replication.NewHandler(replicationService),
		replication:        replicationService,
		processes:          processes,
	}, nil
}

// newShells selects where console shells are started (JD-042).
func newShells(adapter string) (console.Shells, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewShells(), nil
	case config.AdapterFake:
		return fake.NewShells(), nil
	}
	return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
		adapter, config.AdapterFreeBSD, config.AdapterFake)
}

// metricsVersions answers the dashboard's question about FreeBSD versions
// from the jails and templates services, which already judge each one against
// the host (#229). The metrics service reads it at most once a minute.
type metricsVersions struct {
	source    osversion.Source
	jails     *jails.JailService
	templates *templates.Service
}

func (m metricsVersions) Versions(ctx context.Context) (metrics.OSVersions, error) {
	host, err := m.source.HostVersions(ctx)
	if err != nil {
		return metrics.OSVersions{}, err
	}
	out := metrics.OSVersions{Host: host, Jails: make([]metrics.Userland, 0), Templates: make([]metrics.Userland, 0)}
	found, err := m.jails.List(ctx)
	if err != nil {
		return metrics.OSVersions{}, err
	}
	for _, jail := range found {
		if jail.Userland != nil {
			out.Jails = append(out.Jails, metrics.Userland{Name: jail.Name, Standing: *jail.Userland})
		}
	}
	prepared, err := m.templates.List(ctx)
	if err != nil {
		return metrics.OSVersions{}, err
	}
	for _, template := range prepared {
		if template.Userland != nil {
			out.Templates = append(out.Templates, metrics.Userland{
				Name: template.Name, Standing: *template.Userland, SupersededBy: template.SupersededBy,
			})
		}
	}
	return out, nil
}

// vulnTargets is what the package audit audits: every jail by its path, and
// every template with a tree, by its mountpoint (#230).
type vulnTargets struct {
	jails     *jails.JailService
	templates *templates.Service
}

func (v vulnTargets) Jails(ctx context.Context) ([]vulns.Target, error) {
	found, err := v.jails.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]vulns.Target, 0, len(found))
	for _, jail := range found {
		out = append(out, vulns.Target{Name: jail.Name, Root: jail.Path})
	}
	return out, nil
}

func (v vulnTargets) Templates(ctx context.Context) ([]vulns.Target, error) {
	prepared, err := v.templates.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]vulns.Target, 0, len(prepared))
	for _, t := range prepared {
		if t.HasTree() {
			// Its @base, what a clone gets, not its live tree (#251).
			out = append(out, vulns.Target{Name: t.Name, Root: t.Tree()})
		}
	}
	return out, nil
}

// runningJails answers the console's one question of the jails service.
type runningJails struct{ jails *jails.JailService }

func (r runningJails) Running(ctx context.Context, name string) (bool, error) {
	jailName, err := jails.NewJailName(name)
	if err != nil {
		return false, err
	}
	jail, err := r.jails.Get(ctx, jailName)
	if err != nil {
		return false, err
	}
	return jail.Status == jails.JailStatusRunning, nil
}

// guestConsoles answers the console's question of the VMs service: is the
// guest running, and which device is its first console port.
type guestConsoles struct{ vms *vms.Service }

func (g guestConsoles) Console(ctx context.Context, name string) (string, bool, error) {
	detail, err := g.vms.Get(ctx, name)
	if err != nil {
		return "", false, err
	}
	return detail.Consoles["com1"], detail.State == vms.StateRunning, nil
}

// namesInUse is what a suggested name must not be (#148): a jail's, a guest's
// or a template's on this host, read when a suggestion is asked for.
type namesInUse struct {
	jails     *jails.JailService
	vms       *vms.Service
	templates *templates.Service
}

func (n namesInUse) InUse(ctx context.Context) (map[string]bool, error) {
	inUse := map[string]bool{}
	jailList, err := n.jails.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, j := range jailList {
		inUse[j.Name] = true
	}
	listing, err := n.vms.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, vm := range listing.VMs {
		inUse[vm.Name] = true
	}
	templateList, err := n.templates.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range templateList {
		inUse[t.Name] = true
	}
	return inUse, nil
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

// NewNetworkSystem selects the networks implementation (JD-040). Exported for
// `jaildeck init vm-network`, which runs without the rest of the application.
func NewNetworkSystem(adapter string) (networks.System, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewNetworks(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// NetworkSettings is the default VM network as configured.
func NetworkSettings(cfg *config.Config) networks.Settings {
	return networks.Settings{Switch: cfg.VMSwitch, Address: cfg.VMAddress, Egress: cfg.VMEgress, AnchorDir: cfg.PFAnchorDir,
		Range: cfg.NetworkRange}
}

// newImageSystem selects the VM image implementation (JD-037).
func newArchiveSource(adapter string) (housekeeping.ArchiveSource, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewArchiveAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewArchives(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

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

// newVersionSource selects what reads FreeBSD versions (#229), with the same
// refusal to guess as every other adapter choice here.
func newVersionSource(adapter string) (osversion.Source, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewVersions(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newVulnSource selects what lists and audits packages (#230), with the same
// refusal to guess as every other adapter choice here.
func newVulnSource(adapter string) (vulns.Source, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewVulns(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newReplicationSource selects what reads zrepl's replication cursors (#247).
func newReplicationSource(adapter string) (replication.Source, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewReplication(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			adapter, config.AdapterFreeBSD, config.AdapterFake)
	}
}

// newResidentReader selects what reads processes' resident memory (#224).
func newResidentReader(adapter string) (residentReader, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewProcesses(), nil
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

// RequireCredential refuses to start a Jail Deck nobody could sign in to.
func RequireCredential(cfg *config.Config) error {
	return requireCredential(state.NewStore(cfg.StateDir))
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
	limits          jails.LimitChanger
	configReplacer  jails.ConfigReplacer
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
			configReader: a, autostartSource: a, usage: a, exec_: a,
			limits: a, configReplacer: a}, nil
	case config.AdapterFake:
		f := fake.NewJailSystem()
		return jailAdapters{system: f, config: f, remover: f, autostart: f,
			bridges: f, epairs: f, epair: f, rctl: f, devfsRulesets: f,
			configReader: f, autostartSource: f, usage: f, exec_: f,
			limits: f, configReplacer: f}, nil
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
