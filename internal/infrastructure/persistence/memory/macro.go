package memory

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// MacroRepository keeps macros in memory, for the service tests.
type MacroRepository struct {
	mu     sync.Mutex
	macros map[uint]model.Macro
	next   uint
	jobs   map[uint]int
}

// NewMacroRepository creates an empty macro repository.
func NewMacroRepository() *MacroRepository {
	return &MacroRepository{macros: map[uint]model.Macro{}, next: 1}
}

var _ repository.MacroRepository = (*MacroRepository)(nil)

func (r *MacroRepository) GetAll(_ context.Context) ([]*model.Macro, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	macros := make([]*model.Macro, 0, len(r.macros))
	for _, macro := range r.macros {
		macros = append(macros, copyMacro(macro))
	}

	slices.SortFunc(macros, func(a, b *model.Macro) int { return strings.Compare(a.Name, b.Name) })

	return macros, nil
}

func (r *MacroRepository) GetByID(_ context.Context, id uint) (*model.Macro, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	macro, ok := r.macros[id]
	if !ok {
		return nil, apperror.NotFound("macro", strconv.FormatUint(uint64(id), 10))
	}

	return copyMacro(macro), nil
}

func (r *MacroRepository) Save(_ context.Context, macro *model.Macro) (*model.Macro, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.taken(macro.Name, 0) {
		return nil, apperror.Conflictf("a macro named %q already exists", macro.Name).WithCode("macroNameTaken", macro.Name)
	}

	stored := *copyMacro(*macro)
	stored.ID = r.next
	r.next++
	r.macros[stored.ID] = stored

	return copyMacro(stored), nil
}

func (r *MacroRepository) Update(_ context.Context, macro *model.Macro) (*model.Macro, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.macros[macro.ID]; !ok {
		return nil, apperror.NotFound("macro", strconv.FormatUint(uint64(macro.ID), 10))
	}

	if r.taken(macro.Name, macro.ID) {
		return nil, apperror.Conflictf("a macro named %q already exists", macro.Name).WithCode("macroNameTaken", macro.Name)
	}

	r.macros[macro.ID] = *copyMacro(*macro)

	return copyMacro(r.macros[macro.ID]), nil
}

func (r *MacroRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.macros[id]; !ok {
		return apperror.NotFound("macro", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.macros, id)

	return nil
}

func (r *MacroRepository) taken(name string, except uint) bool {
	for id, macro := range r.macros {
		if id != except && macro.Name == name {
			return true
		}
	}

	return false
}

// copyMacro copies a macro down to its steps' parameters, which are maps and
// would otherwise be shared between the caller and the table.
func copyMacro(macro model.Macro) *model.Macro {
	copied := macro
	copied.Actions = make([]model.MacroAction, 0, len(macro.Actions))

	for _, action := range macro.Actions {
		parameters := maps.Clone(action.Parameters)
		if parameters == nil {
			parameters = map[string]string{}
		}

		copied.Actions = append(copied.Actions, model.MacroAction{Type: action.Type, Parameters: parameters})
	}

	return &copied
}

// UseInJobs says how many jobs use the record, since the jobs are kept
// elsewhere.
func (r *MacroRepository) UseInJobs(id uint, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.jobs == nil {
		r.jobs = map[uint]int{}
	}

	r.jobs[id] = count
}

func (r *MacroRepository) CountJobs(_ context.Context, id uint) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.jobs[id], nil
}
