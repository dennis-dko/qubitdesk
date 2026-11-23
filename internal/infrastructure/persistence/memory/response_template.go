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

// ResponseTemplateRepository keeps answer templates in memory, for the service
// tests.
type ResponseTemplateRepository struct {
	mu        sync.Mutex
	templates map[uint]model.ResponseTemplate
	next      uint
}

// NewResponseTemplateRepository creates an empty template repository.
func NewResponseTemplateRepository() *ResponseTemplateRepository {
	return &ResponseTemplateRepository{templates: map[uint]model.ResponseTemplate{}, next: 1}
}

var _ repository.ResponseTemplateRepository = (*ResponseTemplateRepository)(nil)

func (r *ResponseTemplateRepository) GetAll(_ context.Context) ([]*model.ResponseTemplate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	templates := make([]*model.ResponseTemplate, 0, len(r.templates))
	for _, template := range r.templates {
		copied := template
		templates = append(templates, &copied)
	}

	slices.SortFunc(templates, func(a, b *model.ResponseTemplate) int { return strings.Compare(a.Name, b.Name) })

	return templates, nil
}

func (r *ResponseTemplateRepository) GetByID(_ context.Context, id uint) (*model.ResponseTemplate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	template, ok := r.templates[id]
	if !ok {
		return nil, apperror.NotFound("template", strconv.FormatUint(uint64(id), 10))
	}

	return &template, nil
}

func (r *ResponseTemplateRepository) Save(_ context.Context, template *model.ResponseTemplate) (*model.ResponseTemplate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.taken(template.Name, 0) {
		return nil, apperror.Conflictf("a template named %q already exists", template.Name).
			WithCode("templateNameTaken", template.Name)
	}

	stored := *template
	stored.ID = r.next
	r.next++
	r.templates[stored.ID] = stored

	return &stored, nil
}

func (r *ResponseTemplateRepository) Update(_ context.Context, template *model.ResponseTemplate) (*model.ResponseTemplate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.templates[template.ID]; !ok {
		return nil, apperror.NotFound("template", strconv.FormatUint(uint64(template.ID), 10))
	}

	if r.taken(template.Name, template.ID) {
		return nil, apperror.Conflictf("a template named %q already exists", template.Name).
			WithCode("templateNameTaken", template.Name)
	}

	r.templates[template.ID] = *template
	stored := r.templates[template.ID]

	return &stored, nil
}

func (r *ResponseTemplateRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.templates[id]; !ok {
		return apperror.NotFound("template", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.templates, id)

	return nil
}

func (r *ResponseTemplateRepository) CountForQueue(_ context.Context, queueID uint) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0

	for _, template := range r.templates {
		if template.QueueID != nil && *template.QueueID == queueID {
			count++
		}
	}

	return count, nil
}

func (r *ResponseTemplateRepository) taken(name string, except uint) bool {
	for id, template := range r.templates {
		if id != except && template.Name == name {
			return true
		}
	}

	return false
}
