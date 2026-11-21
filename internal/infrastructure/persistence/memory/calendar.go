package memory

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// CalendarRepository keeps calendars in memory, for the service tests.
type CalendarRepository struct {
	mu        sync.Mutex
	calendars map[uint]model.Calendar
	levels    map[uint]int
	next      uint
}

// NewCalendarRepository creates an empty calendar repository.
func NewCalendarRepository() *CalendarRepository {
	return &CalendarRepository{calendars: map[uint]model.Calendar{}, levels: map[uint]int{}, next: 1}
}

var _ repository.CalendarRepository = (*CalendarRepository)(nil)

func (r *CalendarRepository) GetAll(_ context.Context) ([]*model.Calendar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	calendars := make([]*model.Calendar, 0, len(r.calendars))

	for _, calendar := range r.calendars {
		copied := copyCalendar(calendar)
		calendars = append(calendars, &copied)
	}

	slices.SortFunc(calendars, func(a, b *model.Calendar) int { return strings.Compare(a.Name, b.Name) })

	return calendars, nil
}

func (r *CalendarRepository) GetByID(_ context.Context, id uint) (*model.Calendar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	calendar, ok := r.calendars[id]
	if !ok {
		return nil, apperror.NotFound("calendar", strconv.FormatUint(uint64(id), 10))
	}

	copied := copyCalendar(calendar)

	return &copied, nil
}

func (r *CalendarRepository) Save(_ context.Context, calendar *model.Calendar) (*model.Calendar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.taken(calendar.Name, 0) {
		return nil, apperror.Conflictf("a calendar named %q already exists", calendar.Name).
			WithCode("calendarNameTaken", calendar.Name)
	}

	stored := copyCalendar(*calendar)
	stored.ID = r.next
	r.next++
	r.calendars[stored.ID] = stored

	copied := copyCalendar(stored)

	return &copied, nil
}

func (r *CalendarRepository) Update(_ context.Context, calendar *model.Calendar) (*model.Calendar, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.calendars[calendar.ID]; !ok {
		return nil, apperror.NotFound("calendar", strconv.FormatUint(uint64(calendar.ID), 10))
	}

	if r.taken(calendar.Name, calendar.ID) {
		return nil, apperror.Conflictf("a calendar named %q already exists", calendar.Name).
			WithCode("calendarNameTaken", calendar.Name)
	}

	r.calendars[calendar.ID] = copyCalendar(*calendar)

	copied := copyCalendar(*calendar)

	return &copied, nil
}

func (r *CalendarRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.calendars[id]; !ok {
		return apperror.NotFound("calendar", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.calendars, id)

	return nil
}

// CountIn says how many service levels count in a calendar, which are kept
// elsewhere.
func (r *CalendarRepository) CountIn(id uint, levels int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.levels[id] = levels
}

func (r *CalendarRepository) CountServiceLevels(_ context.Context, id uint) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.levels[id], nil
}

func (r *CalendarRepository) taken(name string, except uint) bool {
	for id, calendar := range r.calendars {
		if id != except && calendar.Name == name {
			return true
		}
	}

	return false
}

func copyCalendar(calendar model.Calendar) model.Calendar {
	calendar.Hours = slices.Clone(calendar.Hours)
	calendar.FreeDays = slices.Clone(calendar.FreeDays)

	return calendar
}
