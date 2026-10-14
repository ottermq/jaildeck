package jails

type Jail struct {
	JID      string     `json:"jid"`
	Name     string     `json:"name"`
	Status   JailStatus `json:"status"`
	Hostname string     `json:"hostname"`
	IP       string     `json:"ip"`
	Path     string     `json:"path"`
}

type JailStatus string

const (
	JailStatusRunning JailStatus = "running"
	JailStatusStopped JailStatus = "stopped"
)
