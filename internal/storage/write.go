package storage

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

// Operation names for the audit log. create-dataset is deliberately the same
// string host readiness writes when it builds the layout: an operator filtering
// ?operation=create-dataset wants every dataset Jail Deck ever created, not the
// ones one package happened to create.
const (
	OperationCreateDataset   = "create-dataset"
	OperationSetProperties   = "set-properties"
	OperationInheritProperty = "inherit-property"
)

var ErrDatasetNotFound = common.NewAppError(http.StatusNotFound, "dataset not found")

// DatasetChange is what EnsureDataset did, which is what decides the status
// code: created is 201, a conflict is 409, and anything else is 200.
type DatasetChange struct {
	Dataset string            `json:"dataset"`
	Created bool              `json:"created"`
	Applied map[string]string `json:"applied"`
	// Conflict is set when the dataset exists with a different mountpoint than
	// the one asked for. That is not a property this API will change (JD-023),
	// and it means the caller is describing a dataset other than the one that
	// is there -- worth saying so with both values rather than a bare refusal.
	Conflict *PropertyConflict `json:"conflict,omitempty"`
}

type PropertyConflict struct {
	Property string `json:"property"`
	Want     string `json:"want"`
	Have     string `json:"have"`
}

// EnsureDataset creates name with properties, or brings an existing one to
// them. Idempotent, because an automation client that times out has to be able
// to repeat the call and learn where it stands (JD-016).
func (s *Service) EnsureDataset(ctx context.Context, name string, properties map[string]string) (DatasetChange, error) {
	// Checked against the create list first, which is the wider of the two: a
	// name that is not allowed anywhere should be refused the same way whether
	// or not the dataset happens to exist.
	if err := checkNativeProperties(properties, createProperties, "on a dataset"); err != nil {
		return DatasetChange{}, err
	}

	existing, err := s.datasetNamed(ctx, name)
	if err != nil {
		return DatasetChange{}, err
	}

	if existing == nil {
		result, createErr := s.system.CreateDataset(ctx, name, properties)
		s.logWrite(ctx, OperationCreateDataset, result, createErr)
		if createErr != nil {
			return DatasetChange{}, toWriteError("create dataset", name, createErr)
		}
		return DatasetChange{Dataset: name, Created: true, Applied: nonNil(properties)}, nil
	}

	apply := make(map[string]string, len(properties))
	for property, value := range properties {
		if property == "mountpoint" {
			// Already where it was asked to be: not a change, not a conflict.
			if value == existing.Mountpoint {
				continue
			}
			return DatasetChange{
				Dataset:  name,
				Applied:  map[string]string{},
				Conflict: &PropertyConflict{Property: "mountpoint", Want: value, Have: existing.Mountpoint},
			}, nil
		}
		apply[property] = value
	}
	// This cannot currently fail, and it is kept deliberately. updateProperties
	// is createProperties minus mountpoint, and mountpoint has already been
	// taken out of apply above, so everything left passed the create check. It
	// stays because the two lists are one addition away from differing for
	// real: ZFS has properties that can only ever be set at creation --
	// casesensitivity, normalization, utf8only -- and the day one of those is
	// allowed, this is the line that refuses it on an existing dataset.
	// TestAllowlistsDifferOnlyByMountpoint is what keeps the claim honest.
	if err := checkNativeProperties(apply, updateProperties, "on a dataset that already exists"); err != nil {
		return DatasetChange{}, err
	}
	if len(apply) == 0 {
		return DatasetChange{Dataset: name, Applied: map[string]string{}}, nil
	}

	// Applied unconditionally rather than compared first. zfs reports a size as
	// it chooses to spell it, so deciding "10G is already 10G" would mean this
	// package parsing values it has no business interpreting -- and setting a
	// property to the value it already has is a no-op in ZFS anyway.
	result, setErr := s.system.SetProperties(ctx, name, apply)
	s.logWrite(ctx, OperationSetProperties, result, setErr)
	if setErr != nil {
		return DatasetChange{}, toWriteError("set properties on", name, setErr)
	}
	return DatasetChange{Dataset: name, Applied: apply}, nil
}

// SetUserProperties merges properties into name's user properties: a value sets
// it, a nil value removes it with zfs inherit. It returns what the dataset
// carries afterwards, read back from ZFS rather than assumed (JD-011).
func (s *Service) SetUserProperties(ctx context.Context, name string, properties map[string]*string) (map[string]string, error) {
	set := make(map[string]string, len(properties))
	remove := make([]string, 0, len(properties))
	for property, value := range properties {
		if err := checkUserProperty(property); err != nil {
			return nil, err
		}
		if value == nil {
			remove = append(remove, property)
			continue
		}
		set[property] = *value
	}

	if len(set) > 0 {
		result, err := s.system.SetProperties(ctx, name, set)
		s.logWrite(ctx, OperationSetProperties, result, err)
		if err != nil {
			return nil, toWriteError("set properties on", name, err)
		}
	}
	// One command and one audit line each: zfs inherit takes a single property,
	// so three removals really are three operations and the log says so.
	slices.Sort(remove)
	for _, property := range remove {
		result, err := s.system.InheritProperty(ctx, name, property)
		s.logWrite(ctx, OperationInheritProperty, result, err)
		if err != nil {
			return nil, toWriteError("remove a property from", name, err)
		}
	}

	return s.userPropertiesOf(ctx, name)
}

// datasetNamed returns the dataset or nil, scoped to the dataset itself rather
// than to the pool: one zfs list of one path, which exits non-zero only when
// that path is gone -- and gone is the answer, not an error.
func (s *Service) datasetNamed(ctx context.Context, name string) (*Dataset, error) {
	datasets, err := s.system.DatasetsUnder(ctx, []string{name})
	if err != nil {
		// zfs list exits 1 for a dataset that does not exist, which is what
		// "absent" looks like from here. Telling that apart from a real failure
		// would mean reading stderr, so the next call -- the create -- is left
		// to report anything genuinely wrong.
		return nil, nil
	}
	for i, dataset := range datasets {
		if dataset.Name == name {
			return &datasets[i], nil
		}
	}
	return nil, nil
}

func (s *Service) userPropertiesOf(ctx context.Context, name string) (map[string]string, error) {
	properties, err := s.system.LocalProperties(ctx, []string{name})
	if err != nil {
		return nil, toWriteError("read the properties of", name, err)
	}
	found := map[string]string{}
	for _, property := range properties {
		if property.Dataset == name && isUserProperty(property.Name) {
			found[property.Name] = property.Value
		}
	}
	return found, nil
}

func (s *Service) logWrite(ctx context.Context, operation string, result Result, opErr error) {
	entry := audit.Entry{
		Timestamp: time.Now(),
		Operation: operation,
		Target:    result.Dataset,
		Command:   result.Command,
		Success:   opErr == nil,
	}
	if opErr != nil {
		entry.TimedOut = errors.Is(opErr, context.DeadlineExceeded)
		var cmdErr *system.CommandError
		if errors.As(opErr, &cmdErr) {
			entry.ExitCode = cmdErr.Result.ExitCode
			entry.Error = cmdErr.Unwrap().Error()
		} else {
			entry.Error = opErr.Error()
			entry.ExitCode = -1
		}
	}
	if err := s.operationLogger.Log(ctx, entry); err != nil {
		common.Logger(ctx, "storage").Error("failed to write operation log",
			slog.String("operation", operation),
			slog.String("target", result.Dataset),
			slog.Any("err", err))
	}
}

// toWriteError turns a command failure into something a client can read, with
// zfs's own message in it: zfs says precisely why a recordsize is invalid, and
// nothing this package could write would say it better (JD-012).
func toWriteError(what, name string, err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return common.NewAppError(http.StatusGatewayTimeout,
			"failed to "+what+" "+name+": the command timed out and may have completed")
	}
	message := err.Error()
	var cmdErr *system.CommandError
	if errors.As(err, &cmdErr) {
		message = cmdErr.Summary()
		// zfs ran and refused. Almost always that is the request's fault --
		// "'recordsize' must be power of 2 from 512B to 16M" is not a server
		// error, and a client told 500 will retry a permanently bad value for
		// as long as it is willing to (JD-016).
		//
		// The distinction this can draw reliably is only "ran and exited
		// non-zero" versus "never ran or timed out": ExitCode is -1 for both of
		// those. So a genuinely server-side refusal -- permission denied, a
		// suspended pool -- also reports 400. zfs's own sentence is in the body
		// either way, which is what the operator reads.
		if cmdErr.Result.ExitCode > 0 {
			return common.NewAppError(http.StatusBadRequest, "failed to "+what+" "+name+": "+message)
		}
	}
	return common.NewAppError(http.StatusInternalServerError, "failed to "+what+" "+name+": "+message)
}

// nonNil keeps an absent map out of the JSON as {} rather than null: a client
// iterating the applied properties should not have to check for null first.
func nonNil(properties map[string]string) map[string]string {
	if properties == nil {
		return map[string]string{}
	}
	return properties
}
