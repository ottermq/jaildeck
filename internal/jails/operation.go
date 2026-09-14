package jails

type Operation string

const (
	OperationStart   Operation = "start"
	OperationStop    Operation = "stop"
	OperationRestart Operation = "restart"
)

func (o Operation) String() string { return string(o) }

func (o Operation) DesiredStatus() JailStatus {
	switch o {
	case OperationStart, OperationRestart:
		return JailStatusRunning
	case OperationStop:
		return JailStatusStopped
	default:
		return ""
	}

}
