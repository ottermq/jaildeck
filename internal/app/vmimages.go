package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/images"
	"github.com/ottermq/jaildeck/internal/vms"
)

// vmImageCatalog lets the vms domain read the images domain's templates
// without either importing the other (JD-038).
type vmImageCatalog struct {
	images *images.Service
}

func (c vmImageCatalog) Image(ctx context.Context, name string) (vms.ImageRef, error) {
	image, err := c.images.Get(ctx, name)
	var appErr *common.AppError
	if errors.As(err, &appErr) && (appErr.Code == http.StatusNotFound || appErr.Code == http.StatusBadRequest) {
		return vms.ImageRef{}, vms.ErrNoSuchImage
	}
	if err != nil {
		return vms.ImageRef{}, err
	}
	// Being prepared is not ready, whatever the zvol already says.
	return vms.ImageRef{Ready: image.Ready && image.Running == nil, Snapshot: image.Snapshot, Size: image.Size}, nil
}
