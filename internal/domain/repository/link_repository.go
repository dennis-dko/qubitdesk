package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// LinkRepository keeps the links between records. A record's own repository
// takes its links with it when the record is deleted, in the same transaction,
// because a link names its ends by kind and id and no key can follow them.
type LinkRepository interface {
	// Of is every link with the record at either end, the oldest first.
	Of(ctx context.Context, object model.LinkObject, id uint) ([]*model.Link, error)

	GetByID(ctx context.Context, id uint) (*model.Link, error)
	Create(ctx context.Context, link *model.Link) (*model.Link, error)
	Delete(ctx context.Context, id uint) error
}
