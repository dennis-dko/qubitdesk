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

// ExecPlanRepository keeps execution plans in memory, for the service tests.
type ExecPlanRepository struct {
	mu    sync.Mutex
	plans map[uint]model.ExecPlan
	next  uint
}

// NewExecPlanRepository creates an empty plan repository.
func NewExecPlanRepository() *ExecPlanRepository {
	return &ExecPlanRepository{plans: map[uint]model.ExecPlan{}, next: 1}
}

var _ repository.ExecPlanRepository = (*ExecPlanRepository)(nil)

func (r *ExecPlanRepository) GetAll(_ context.Context) ([]*model.ExecPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	plans := make([]*model.ExecPlan, 0, len(r.plans))
	for _, plan := range r.plans {
		plans = append(plans, copyPlan(plan))
	}

	slices.SortFunc(plans, func(a, b *model.ExecPlan) int { return strings.Compare(a.Name, b.Name) })

	return plans, nil
}

func (r *ExecPlanRepository) GetByID(_ context.Context, id uint) (*model.ExecPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	plan, ok := r.plans[id]
	if !ok {
		return nil, apperror.NotFound("plan", strconv.FormatUint(uint64(id), 10))
	}

	return copyPlan(plan), nil
}

func (r *ExecPlanRepository) Save(_ context.Context, plan *model.ExecPlan) (*model.ExecPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.taken(plan.Name, 0) {
		return nil, apperror.Conflictf("an execution plan named %q already exists", plan.Name).
			WithCode("planNameTaken", plan.Name)
	}

	stored := *copyPlan(*plan)
	stored.ID = r.next
	r.next++
	r.plans[stored.ID] = stored

	return copyPlan(stored), nil
}

func (r *ExecPlanRepository) Update(_ context.Context, plan *model.ExecPlan) (*model.ExecPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.plans[plan.ID]; !ok {
		return nil, apperror.NotFound("plan", strconv.FormatUint(uint64(plan.ID), 10))
	}

	if r.taken(plan.Name, plan.ID) {
		return nil, apperror.Conflictf("an execution plan named %q already exists", plan.Name).
			WithCode("planNameTaken", plan.Name)
	}

	r.plans[plan.ID] = *copyPlan(*plan)

	return copyPlan(r.plans[plan.ID]), nil
}

func (r *ExecPlanRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.plans[id]; !ok {
		return apperror.NotFound("plan", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.plans, id)

	return nil
}

func (r *ExecPlanRepository) taken(name string, except uint) bool {
	for id, plan := range r.plans {
		if id != except && plan.Name == name {
			return true
		}
	}

	return false
}

// copyPlan copies a plan with lists of its own, which the caller and the table
// would otherwise share.
func copyPlan(plan model.ExecPlan) *model.ExecPlan {
	copied := plan
	copied.Weekdays = slices.Clone(plan.Weekdays)
	copied.Times = slices.Clone(plan.Times)
	copied.Events = slices.Clone(plan.Events)

	return &copied
}
