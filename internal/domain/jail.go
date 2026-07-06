package domain

type JailStatus string

const (
	JailStatusRunning JailStatus = "running"
	JailStatusStopped JailStatus = "stopped"
)

type Jail struct {
	JID      string
	Name     string
	Status   JailStatus
	Hostname string
	IP       string
	Path     string
}
