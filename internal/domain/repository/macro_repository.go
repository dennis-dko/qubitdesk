package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// MacroRepository keeps the macros, each with its steps in the order they are
// taken; saving a macro writes its steps in place of the ones it had.
type MacroRepository interface {
	GetAll(ctx context.Context) ([]*model.Macro, error)
	GetByID(ctx context.Context, id uint) (*model.Macro, error)
	Save(ctx context.Context, macro *model.Macro) (*model.Macro, error)
	Update(ctx context.Context, macro *model.Macro) (*model.Macro, error)
	Delete(ctx context.Context, id uint) error

	// CountJobs reports how many jobs run the macro, so that one in use is
	// retired rather than deleted from under them.
	CountJobs(ctx context.Context, id uint) (int, error)
}
