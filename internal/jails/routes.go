package jails

import "github.com/go-chi/chi/v5"

func RegisterJailRoutes(r chi.Router, h *JailHandler) {
	r.Route("/jails", func(r chi.Router) {

		r.Get("/", h.List)

		r.Route("/{name}", func(r chi.Router) {
			r.Get("/", h.Get)
			r.Post("/start", h.Start)
			r.Post("/stop", h.Stop)
			r.Post("/restart", h.Restart)

			// The jail's own dataset's user properties, and its children's.
			// PATCH rather than PUT: these merge, and a PUT that replaced the
			// whole set would strip a tag another tool had set.
			r.Patch("/properties", h.SetProperties)

			// Snapshots of the jail's own dataset are taken, and destroyed,
			// together with its children's: one zfs snapshot -r at one point
			// in time, which is what an upgrade path needs (JD-007). Rollback
			// is the exception and is never recursive, because zfs rollback
			// cannot descend into children at all.
			// Replacing the root is a jail operation, not a dataset one: it
			// stops and starts the jail around the move.
			r.Post("/reclone", h.Reclone)
			// Cloning the jail as a template (Handbook 17.4.3). It lives on
			// the jail because the jail is the input and because whether it
			// is running is this domain's to know.
			r.Post("/template", h.MakeTemplate)

			r.Get("/snapshots", h.ListSnapshots)
			r.Put("/snapshots/{snapshot}", h.CreateSnapshot)
			r.Delete("/snapshots/{snapshot}", h.DestroySnapshot)
			r.Post("/snapshots/{snapshot}/rollback", h.Rollback)

			r.Route("/datasets", func(r chi.Router) {
				r.Put("/{child}", h.EnsureDataset)
				r.Patch("/{child}/properties", h.SetDatasetProperties)
				// Moving a dataset out of the jail, and back in. POST rather
				// than PUT or DELETE: neither creates nor removes anything,
				// and a dataset that leaves a jail is the same dataset.
				r.Post("/{child}/retain", h.Retain)
				r.Post("/{child}/restore", h.Restore)
				r.Put("/{child}/snapshots/{snapshot}", h.CreateDatasetSnapshot)
				r.Delete("/{child}/snapshots/{snapshot}", h.DestroyDatasetSnapshot)
				r.Post("/{child}/snapshots/{snapshot}/rollback", h.RollbackDataset)
			})
		})
	})
}
