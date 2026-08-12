package services

import (
	"context"
	"strings"

	"github.com/ottermq/jaildeck/internal/operations"
)

type OperationService struct {
	reader operations.Reader
}

func NewOperationService(reader operations.Reader) *OperationService {
	return &OperationService{reader: reader}
}

func (s *OperationService) Recent(ctx context.Context, limit int, mapFilter map[string]string) ([]operations.Entry, error) {
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

	filter := operations.Filter{
		Operation: operation,
		Success:   success,
		Targets:   targets,
	}
	return s.reader.Recent(ctx, limit, filter)
}
