package audit

import (
	"context"
	"strings"
)

type AuditService struct {
	reader Reader
}

func NewOperationService(reader Reader) *AuditService {
	return &AuditService{reader: reader}
}

func (s *AuditService) Recent(ctx context.Context, limit int, mapFilter map[string]string) ([]Entry, error) {
	operation := strings.ToLower(mapFilter["operation"])

	var success *bool
	switch mapFilter["success"] {
	case "true", "false":
		temp := mapFilter["success"] == "true"
		success = &temp
	}

	var targets []string
	qTargets := strings.TrimSpace(mapFilter["targets"])
	if qTargets != "" {
		tmp := strings.Split(qTargets, ",")
		for _, t := range tmp {
			t = strings.TrimSpace(t)
			if len(t) > 0 {
				targets = append(targets, t)
			}
		}
	}

	filter := Filter{
		Operation: operation,
		Success:   success,
		Targets:   targets,
	}
	return s.reader.Recent(ctx, limit, filter)
}
