package jails

import (
	"context"
)

type OperationResult struct {
	Jail    Jail
	Command string
}

type JailSystem interface {
	List(ctx context.Context) ([]Jail, error)
	Start(ctx context.Context, name string) (OperationResult, error)
	Stop(ctx context.Context, name string) (OperationResult, error)
	Restart(ctx context.Context, name string) (OperationResult, error)
}
