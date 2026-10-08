package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"codeberg.org/Otter-Labs/jaildeck/internal/audit"
	"codeberg.org/Otter-Labs/jaildeck/internal/auth"
	"codeberg.org/Otter-Labs/jaildeck/internal/config"
	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
	"codeberg.org/Otter-Labs/jaildeck/internal/state"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
	"codeberg.org/Otter-Labs/jaildeck/internal/system/fake"
	"codeberg.org/Otter-Labs/jaildeck/internal/system/freebsd"
)

var ErrNoCredential = errors.New("no operator credential configured")

type App struct {
	authHandler  *auth.AuthHandler
	jailHandler  *jails.JailHandler
	auditHandler *audit.AuditHandler
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

	jailService := jails.NewJailService(jailSystem, auditLogger)
	jailHandler := jails.NewJailHandler(jailService)

	auditService := audit.NewAuditService(auditLogger)
	auditHandler := audit.NewAuditHandler(auditService)

	return &App{
		authHandler:  authHandler,
		jailHandler:  jailHandler,
		auditHandler: auditHandler,
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
