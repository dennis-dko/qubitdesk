package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// CalendarRepository keeps the calendars, each with its working hours and its
// free days, which are written and read with it.
type CalendarRepository interface {
	GetAll(ctx context.Context) ([]*model.Calendar, error)
	GetByID(ctx context.Context, id uint) (*model.Calendar, error)
	Save(ctx context.Context, calendar *model.Calendar) (*model.Calendar, error)

	// Update replaces a calendar's hours and free days with the ones it carries.
	Update(ctx context.Context, calendar *model.Calendar) (*model.Calendar, error)

	Delete(ctx context.Context, id uint) error

	// CountServiceLevels is how many service levels count in the calendar.
	CountServiceLevels(ctx context.Context, id uint) (int, error)
}
