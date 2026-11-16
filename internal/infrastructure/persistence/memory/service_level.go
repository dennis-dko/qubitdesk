package memory

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ServiceLevelRepository keeps service levels in memory, for the service tests.
// What uses one is told to it with UseIn, since the queues and tickets that
// would are kept elsewhere.
type ServiceLevelRepository struct {
	mu     sync.Mutex
	levels map[uint]model.ServiceLevel
	uses   map[uint][2]int
	next   uint
}

// NewServiceLevelRepository creates an empty service level repository.
func NewServiceLevelRepository() *ServiceLevelRepository {
	return &ServiceLevelRepository{levels: map[uint]model.ServiceLevel{}, uses: map[uint][2]int{}, next: 1}
}

var _ repository.ServiceLevelRepository = (*ServiceLevelRepository)(nil)

// UseIn says how many queues start their tickets with a service level and how
// many tickets carry it.
func (r *ServiceLevelRepository) UseIn(id uint, queues, tickets int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.uses[id] = [2]int{queues, tickets}
}

func (r *ServiceLevelRepository) GetAll(_ context.Context) ([]*model.ServiceLevel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	levels := make([]*model.ServiceLevel, 0, len(r.levels))

	for _, level := range r.levels {
		copied := level
		levels = append(levels, &copied)
	}

	slices.SortFunc(levels, func(a, b *model.ServiceLevel) int { return strings.Compare(a.Name, b.Name) })

	return levels, nil
}

func (r *ServiceLevelRepository) GetByID(_ context.Context, id uint) (*model.ServiceLevel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	level, ok := r.levels[id]
	if !ok {
		return nil, apperror.NotFound("sla", strconv.FormatUint(uint64(id), 10))
	}

	return &level, nil
}

func (r *ServiceLevelRepository) Save(_ context.Context, level *model.ServiceLevel) (*model.ServiceLevel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.taken(level.Name, 0) {
		return nil, apperror.Conflictf("a service level named %q already exists", level.Name).
			WithCode("serviceLevelNameTaken", level.Name)
	}

	stored := *level
	stored.ID = r.next
	r.next++
	r.levels[stored.ID] = stored

	return &stored, nil
}

func (r *ServiceLevelRepository) Update(_ context.Context, level *model.ServiceLevel) (*model.ServiceLevel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.levels[level.ID]; !ok {
		return nil, apperror.NotFound("sla", strconv.FormatUint(uint64(level.ID), 10))
	}

	if r.taken(level.Name, level.ID) {
		return nil, apperror.Conflictf("a service level named %q already exists", level.Name).
			WithCode("serviceLevelNameTaken", level.Name)
	}

	r.levels[level.ID] = *level
	stored := *level

	return &stored, nil
}

func (r *ServiceLevelRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.levels[id]; !ok {
		return apperror.NotFound("sla", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.levels, id)

	return nil
}

func (r *ServiceLevelRepository) CountUses(_ context.Context, id uint) (int, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	uses := r.uses[id]

	return uses[0], uses[1], nil
}

func (r *ServiceLevelRepository) taken(name string, except uint) bool {
	for id, level := range r.levels {
		if id != except && level.Name == name {
			return true
		}
	}

	return false
}
