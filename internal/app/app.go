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
)

var ErrNoCredential = errors.New("no operator credential configured")

type App struct {
	authHandler    *auth.AuthHandler
	jailHandler    *jails.JailHandler
	hostHandler    *host.HostHandler
	auditHandler   *audit.AuditHandler
	storageHandler *storage.StorageHandler
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

	jailSystem, err := newJailSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}

	storageSystem, err := newStorageSystem(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	storageService := storage.NewService(storageSystem, auditLogger, cfg.ZFSPool)

	storageHandler := storage.NewStorageHandler(storageService)

	jailStorage := storage.NewJailSource(storageService)
	jailService := jails.NewJailService(jailSystem, auditLogger, jailStorage, jailStorage, jailStorage, jailStorage, jailStorage)
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
		authHandler:    authHandler,
		jailHandler:    jailHandler,
		hostHandler:    hostHandler,
		auditHandler:   auditHandler,
		storageHandler: storageHandler,
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

// newJailSystem selects the jails.JailSystem implementation named by adapter.
// An unknown name is an error rather than a silent fallback: which adapter is
// running determines whether real jails are touched.
func newJailSystem(adapter string) (jails.JailSystem, error) {
	switch adapter {
	case config.AdapterFreeBSD:
		return freebsd.NewAdapter(system.NewExecCommandRunner()), nil
	case config.AdapterFake:
		return fake.NewJailSystem(), nil
	default:
		return nil, fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
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
