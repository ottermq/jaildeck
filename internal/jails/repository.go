package jails

import (
	"context"
)

type JailSystem interface {
	List(ctx context.Context) ([]Jail, error)
	Start(ctx context.Context, name string) (Jail, error)
	Stop(ctx context.Context, name string) (Jail, error)
	Restart(ctx context.Context, name string) (Jail, error)
}
