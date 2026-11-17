package service

import (
	"context"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ResponseTemplateApplicationService administers the answer templates - KIX18's
// text modules - and hands an agent the ones offered for a ticket, with the
// ticket's placeholders filled in the agent's words. What a template becomes is
// the agent's to change before it is sent: it starts an article, it does not
// write one.
type ResponseTemplateApplicationService struct {
	templates repository.ResponseTemplateRepository
	tickets   TicketService
	refs      MacroReferences
}

// NewResponseTemplateApplicationService reads a ticket through tickets, so that
// a ticket the caller may not read is not found, and the queue a template names
// and what a ticket's placeholders write through refs.
func NewResponseTemplateApplicationService(
	templates repository.ResponseTemplateRepository,
	tickets TicketService,
	refs MacroReferences,
) *ResponseTemplateApplicationService {
	return &ResponseTemplateApplicationService{templates: templates, tickets: tickets, refs: refs}
}

func (s *ResponseTemplateApplicationService) List(ctx context.Context) ([]*model.ResponseTemplate, error) {
	return s.templates.GetAll(ctx)
}

func (s *ResponseTemplateApplicationService) Get(ctx context.Context, id uint) (*model.ResponseTemplate, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.templates.GetByID(ctx, id)
}

func (s *ResponseTemplateApplicationService) Create(
	ctx context.Context,
	cmd command.ResponseTemplateCommand,
) (*model.ResponseTemplate, error) {
	var missing []string

	if cmd.Name == nil {
		missing = append(missing, "name")
	}

	if cmd.Body == nil {
		missing = append(missing, "body")
	}

	if len(missing) > 0 {
		return nil, apperror.InvalidFields(missing...)
	}

	template := &model.ResponseTemplate{Valid: true}
	if err := s.apply(ctx, template, cmd); err != nil {
		return nil, err
	}

	return s.templates.Save(ctx, template)
}

func (s *ResponseTemplateApplicationService) Update(
	ctx context.Context,
	id uint,
	cmd command.ResponseTemplateCommand,
) (*model.ResponseTemplate, error) {
	template, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.apply(ctx, template, cmd); err != nil {
		return nil, err
	}

	return s.templates.Update(ctx, template)
}

func (s *ResponseTemplateApplicationService) Delete(ctx context.Context, id uint) error {
	if id == 0 {
		return apperror.InvalidFields("id")
	}

	return s.templates.Delete(ctx, id)
}

// ForTicket is the templates offered for a ticket the caller may read, the
// ticket's placeholders filled in the caller's language. A ticket the caller may
// not read is not found, as everywhere else.
func (s *ResponseTemplateApplicationService) ForTicket(
	ctx context.Context,
	caller *Principal,
	ticketID uint,
) ([]model.ResponseTemplate, error) {
	ticket, err := s.tickets.GetTicket(ctx, caller, ticketID)
	if err != nil {
		return nil, err
	}

	templates, err := s.templates.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	around, _, err := surroundingsOf(ctx, s.refs, ticket)
	if err != nil {
		return nil, err
	}

	language := ""
	if caller != nil && caller.User != nil {
		language = caller.User.Language
	}

	words := around.words(language)
	offered := make([]model.ResponseTemplate, 0, len(templates))

	for _, template := range templates {
		if !template.OfferedIn(ticket.QueueID) {
			continue
		}

		filled := *template
		filled.Subject = shortened(fillTags(template.Subject, words), model.MaxTicketTitleLength)
		filled.Body = fillTags(template.Body, words)
		offered = append(offered, filled)
	}

	return offered, nil
}

// apply writes a command into a template, refusing every field it cannot hold
// at once.
func (s *ResponseTemplateApplicationService) apply(
	ctx context.Context,
	template *model.ResponseTemplate,
	cmd command.ResponseTemplateCommand,
) error {
	var invalid []string

	name := strings.TrimSpace(valueOf(cmd.Name))
	if cmd.Name != nil && (name == "" || model.TooLong(name, model.MaxNameLength)) {
		invalid = append(invalid, "name")
	}

	subject := strings.TrimSpace(valueOf(cmd.Subject))
	if model.TooLong(subject, model.MaxTicketTitleLength) {
		invalid = append(invalid, "subject")
	}

	body := strings.TrimSpace(valueOf(cmd.Body))
	if cmd.Body != nil && (body == "" || model.TooLong(body, model.MaxTemplateBodyLength)) {
		invalid = append(invalid, "body")
	}

	if cmd.QueueID != nil {
		if _, err := s.refs.Queues.GetByID(ctx, *cmd.QueueID); err != nil {
			if apperror.KindOf(err) != apperror.KindNotFound {
				return err
			}

			invalid = append(invalid, "queueId")
		}
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if cmd.Name != nil {
		template.Name = name
	}

	if cmd.Subject != nil {
		template.Subject = subject
	}

	if cmd.Body != nil {
		template.Body = body
	}

	switch {
	case cmd.ClearQueue:
		template.QueueID = nil
	case cmd.QueueID != nil:
		queue := *cmd.QueueID
		template.QueueID = &queue
	}

	if cmd.Valid != nil {
		template.Valid = *cmd.Valid
	}

	return nil
}
