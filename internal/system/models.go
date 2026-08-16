package system

type Jail struct {
	JID      string
	Name     string
	Status   JailStatus
	Hostname string
	IP       string
	Path     string
}

type JailStatus string

const (
	JailStatusRunning JailStatus = "running"
	JailStatusStopped JailStatus = "stopped"
)
