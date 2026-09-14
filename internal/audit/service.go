package audit

import (
	"context"
)

type AuditService struct {
	reader Reader
}

func NewAuditService(reader Reader) *AuditService {
	return &AuditService{reader: reader}
}

func (s *AuditService) Recent(ctx context.Context, limit int, filter Filter) ([]Entry, error) {
	return s.reader.Recent(ctx, limit, filter)
}
