package app

import (
	"context"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/events"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/vms"
)

// liveAudit writes every audit entry and then announces it on the stream
// (#124, JD-052). Never before: the log is the record, and an event never
// claims what the log does not hold.
type liveAudit struct {
	next   audit.Logger
	broker *events.Broker
}

func (l liveAudit) Log(ctx context.Context, entry audit.Entry) error {
	if err := l.next.Log(ctx, entry); err != nil {
		return err
	}
	l.broker.Operated(entry.Target)
	l.broker.Publish(events.TypeOperation, events.Operation{Operation: entry.Operation, Target: entry.Target,
		Success: entry.Success, RequestID: entry.RequestID, Timestamp: entry.Timestamp})
	return nil
}

// liveInstances is every jail and guest with its state, for the observer.
// No vm-bhyve is no guests, not an error.
type liveInstances struct {
	jails *jails.JailService
	vms   *vms.Service
}

func (l liveInstances) Instances(ctx context.Context) ([]events.Instance, error) {
	states, err := l.jails.JailStates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]events.Instance, 0, len(states))
	for name, up := range states {
		state := "stopped"
		if up {
			state = "running"
		}
		out = append(out, events.Instance{Kind: "jail", Name: name, State: state})
	}
	listing, err := l.vms.List(ctx)
	if err != nil {
		return nil, err
	}
	if listing.VMBhyve == vms.VMBhyveReady {
		for _, vm := range listing.VMs {
			out = append(out, events.Instance{Kind: "vm", Name: vm.Name, State: vm.State})
		}
	}
	return out, nil
}
