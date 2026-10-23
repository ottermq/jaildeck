package jails

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
}

type JailStatus string

const (
	JailStatusRunning JailStatus = "running"
	JailStatusStopped JailStatus = "stopped"
)
