package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// ResponseTemplateRepository keeps the answer templates.
type ResponseTemplateRepository interface {
	GetAll(ctx context.Context) ([]*model.ResponseTemplate, error)
	GetByID(ctx context.Context, id uint) (*model.ResponseTemplate, error)
	Save(ctx context.Context, template *model.ResponseTemplate) (*model.ResponseTemplate, error)
	Update(ctx context.Context, template *model.ResponseTemplate) (*model.ResponseTemplate, error)
	Delete(ctx context.Context, id uint) error

	// CountForQueue reports how many templates are offered in the queue alone, so
	// that a queue they name is not deleted from under them.
	CountForQueue(ctx context.Context, queueID uint) (int, error)
}
