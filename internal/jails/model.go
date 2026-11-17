package jails

import "github.com/ottermq/jaildeck/internal/osversion"

type Jail struct {
	JID      string     `json:"jid"`
	Name     string     `json:"name"`
	Status   JailStatus `json:"status"`
	Hostname string     `json:"hostname"`
	IP       string     `json:"ip"`
	Path     string     `json:"path"`
	// Datasets and Properties are filled by the listing and by GET
	// /api/jails/{name}, and left out everywhere else. omitempty rather than an
	// empty collection on purpose: a start or restart response reports what the
	// operation knew, and claiming "datasets: []" there would say the jail has
	// none when the truth is that nothing looked.
	Datasets   []Dataset         `json:"datasets,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	// Userland is the jail's FreeBSD userland and how it stands against the
	// host's (#229), read from the jail's own files and never by running
	// anything in it (JD-060). Filled where Datasets is, for the same reason.
	Userland *osversion.Standing `json:"userland,omitempty"`
}

type JailStatus string

const (
	JailStatusRunning JailStatus = "running"
	JailStatusStopped JailStatus = "stopped"
)
