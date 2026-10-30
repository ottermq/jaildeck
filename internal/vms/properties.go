package vms

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
)

// SetProperties is set after construction, as the consoles are: the storage
// domain reads and writes a guest's labels (JD-044).
func (s *Service) SetProperties(store PropertyStore) { s.properties = store }

// labelled fills in each guest's labels, in one read for all of them. A
// failure costs the labels and nothing else, as the switches' does: the
// guests are what the listing is for.
func (s *Service) labelled(ctx context.Context, guests []VM) {
	for i := range guests {
		guests[i].Properties = map[string]string{}
	}
	if s.properties == nil || len(guests) == 0 {
		return
	}
	stores, err := s.system.VMDatasets(ctx)
	if err != nil {
		common.Logger(ctx, "vms").Warn("could not read vm-bhyve's datastores", slog.Any("err", err))
		return
	}
	datasets := make([]string, 0, len(guests))
	for _, guest := range guests {
		if dataset := guestDataset(stores, guest); dataset != "" {
			datasets = append(datasets, dataset)
		}
	}
	if len(datasets) == 0 {
		return
	}
	found, err := s.properties.UserPropertiesOf(ctx, datasets)
	if err != nil {
		common.Logger(ctx, "vms").Warn("could not read the guests' labels", slog.Any("err", err))
		return
	}
	for i, guest := range guests {
		if labels, ok := found[guestDataset(stores, guest)]; ok {
			guests[i].Properties = labels
		}
	}
}

// guestDataset is <datastore's dataset>/<name>, empty when the datastore is
// not on ZFS. vm-bhyve makes it at create and destroys it at destroy, and a
// rename carries it and its properties (measured, JD-044).
func guestDataset(stores map[string]string, guest VM) string {
	if dataset := stores[guest.Datastore]; dataset != "" {
		return dataset + "/" + guest.Name
	}
	return ""
}

// UpdateProperties labels a guest: ZFS user properties on its dataset, a nil
// value removing one (JD-044). The same rules as a jail's -- a name with a
// colon, merged rather than replaced -- because the same storage call makes
// both. Running or not makes no difference: zfs sets a property on a running
// guest's dataset, measured.
func (s *Service) UpdateProperties(ctx context.Context, name string, properties map[string]*string) (map[string]string, error) {
	if s.system == nil {
		return nil, s.unavailable()
	}
	if s.properties == nil {
		return nil, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck is running without the storage access it needs to label a guest")
	}
	guest, err := s.guest(ctx, name)
	if err != nil {
		return nil, err
	}
	stores, err := s.system.VMDatasets(ctx)
	if err != nil {
		return nil, readError("vm-bhyve's datastores", err)
	}
	dataset := guestDataset(stores, *guest)
	if dataset == "" {
		return nil, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s is in the datastore %s, which is not on ZFS; a guest's labels are ZFS user properties "+
				"on its dataset, so it has nowhere to keep one", name, guest.Datastore))
	}
	// A write is not abandoned because the client hung up (JD-014).
	return s.properties.SetUserProperties(context.WithoutCancel(ctx), dataset, properties)
}
