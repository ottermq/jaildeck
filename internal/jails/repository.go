package jails

import (
	"context"

	"github.com/ottermq/jaildeck/internal/system"
)

type JailSystem interface {
	List(ctx context.Context) ([]system.Jail, error)
	Start(ctx context.Context, name string) (system.Jail, error)
	Stop(ctx context.Context, name string) (system.Jail, error)
	Restart(ctx context.Context, name string) (system.Jail, error)
}
