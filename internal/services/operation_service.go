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
	qTargets := mapFilter["targets"]
	if qTargets != "" {
		qTargets = strings.ReplaceAll(qTargets, " ", "")
		targets = strings.Split(qTargets, ",")
	}

	filter := operations.Filter{
		Operation: operation,
		Success:   success,
		Targets:   targets,
	}
	return s.reader.Recent(ctx, limit, filter)
}
