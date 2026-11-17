package service

import (
	"context"
	"slices"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// NotificationDefinitionApplicationService administers the notifications: what
// the agents around a ticket are told when something happens to it, and in
// whose words. A notification is checked as it is saved - events there are,
// criteria naming records that are there, recipients there are, and a message in
// a language the installation speaks for each language it says something in -
// so that one which saves is one that can be sent.
type NotificationDefinitionApplicationService struct {
	definitions repository.NotificationDefinitionRepository
	refs        MacroReferences
}

// NewNotificationDefinitionApplicationService reads the records a
// notification's criteria may name through refs.
func NewNotificationDefinitionApplicationService(
	definitions repository.NotificationDefinitionRepository,
	refs MacroReferences,
) *NotificationDefinitionApplicationService {
	return &NotificationDefinitionApplicationService{definitions: definitions, refs: refs}
}

func (s *NotificationDefinitionApplicationService) List(ctx context.Context) ([]*model.NotificationDefinition, error) {
	return s.definitions.GetAll(ctx)
}

func (s *NotificationDefinitionApplicationService) Get(ctx context.Context, id uint) (*model.NotificationDefinition, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.definitions.GetByID(ctx, id)
}

func (s *NotificationDefinitionApplicationService) Create(
	ctx context.Context,
	cmd command.NotificationDefinitionCommand,
) (*model.NotificationDefinition, error) {
	var missing []string

	for _, field := range []struct {
		name string
		sent bool
	}{
		{"name", cmd.Name != nil}, {"events", cmd.Events != nil}, {"recipients", cmd.Recipients != nil},
		{"messages", cmd.Messages != nil},
	} {
		if !field.sent {
			missing = append(missing, field.name)
		}
	}

	if len(missing) > 0 {
		return nil, apperror.InvalidFields(missing...)
	}

	definition := &model.NotificationDefinition{Valid: true}
	if err := s.apply(ctx, definition, cmd); err != nil {
		return nil, err
	}

	return s.definitions.Save(ctx, definition)
}

func (s *NotificationDefinitionApplicationService) Update(
	ctx context.Context,
	id uint,
	cmd command.NotificationDefinitionCommand,
) (*model.NotificationDefinition, error) {
	definition, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.apply(ctx, definition, cmd); err != nil {
		return nil, err
	}

	return s.definitions.Update(ctx, definition)
}

// Delete deletes a notification. What it already told somebody stays told: the
// notifications sent keep their own words.
func (s *NotificationDefinitionApplicationService) Delete(ctx context.Context, id uint) error {
	if id == 0 {
		return apperror.InvalidFields("id")
	}

	return s.definitions.Delete(ctx, id)
}

// apply writes a command into a notification, refusing every field it cannot
// hold at once.
func (s *NotificationDefinitionApplicationService) apply(
	ctx context.Context,
	definition *model.NotificationDefinition,
	cmd command.NotificationDefinitionCommand,
) error {
	var invalid []string

	name := strings.TrimSpace(valueOf(cmd.Name))
	if cmd.Name != nil && (name == "" || model.TooLong(name, model.MaxCatalogNameLength)) {
		invalid = append(invalid, "name")
	}

	description := strings.TrimSpace(valueOf(cmd.Description))
	if model.TooLong(description, model.MaxDescriptionLength) {
		invalid = append(invalid, "description")
	}

	events := definition.Events
	if cmd.Events != nil {
		var ok bool
		if events, ok = notificationEventsOf(*cmd.Events); !ok || len(events) == 0 {
			invalid = append(invalid, "events")
		}
	}

	criteria := definition.Criteria
	if cmd.Criteria != nil {
		criteria = model.TicketCriteria{
			QueueIDs: distinct(cmd.Criteria.QueueIDs), StateIDs: distinct(cmd.Criteria.StateIDs),
			PriorityIDs: distinct(cmd.Criteria.PriorityIDs), TypeIDs: distinct(cmd.Criteria.TypeIDs),
			Locked: cmd.Criteria.Locked,
		}

		named, err := criteriaNameWhatIsThere(ctx, s.refs, criteria)
		if err != nil {
			return err
		}

		if !named {
			invalid = append(invalid, "criteria")
		}
	}

	recipients := definition.Recipients
	if cmd.Recipients != nil {
		var ok bool
		if recipients, ok = recipientsOf(*cmd.Recipients); !ok || len(recipients) == 0 {
			invalid = append(invalid, "recipients")
		}
	}

	messages := definition.Messages
	if cmd.Messages != nil {
		var ok bool
		if messages, ok = messagesOf(*cmd.Messages); !ok || len(messages) == 0 {
			invalid = append(invalid, "messages")
		}
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if cmd.Name != nil {
		definition.Name = name
	}

	if cmd.Description != nil {
		definition.Description = description
	}

	if cmd.Valid != nil {
		definition.Valid = *cmd.Valid
	}

	if cmd.OncePerDay != nil {
		definition.OncePerDay = *cmd.OncePerDay
	}

	if cmd.Optional != nil {
		definition.Optional = *cmd.Optional
	}

	definition.Events, definition.Criteria, definition.Recipients, definition.Messages = events, criteria, recipients,
		messages

	return nil
}

// notificationEventsOf is the events sent, each once and in the order a form
// offers them; an event there is not cannot be held.
func notificationEventsOf(sent []string) ([]model.TicketEvent, bool) {
	events := make([]model.TicketEvent, 0, len(sent))

	for _, event := range model.TicketEvents() {
		if slices.ContainsFunc(sent, func(named string) bool { return strings.TrimSpace(named) == string(event) }) {
			events = append(events, event)
		}
	}

	return events, len(events) == len(distinctStrings(sent))
}

// recipientsOf is the recipients sent, each once and in the order a form offers
// them; a recipient there is not cannot be held.
func recipientsOf(sent []string) ([]model.NotificationRecipient, bool) {
	recipients := make([]model.NotificationRecipient, 0, len(sent))

	for _, recipient := range model.NotificationRecipients() {
		if slices.ContainsFunc(sent, func(named string) bool { return strings.TrimSpace(named) == string(recipient) }) {
			recipients = append(recipients, recipient)
		}
	}

	return recipients, len(recipients) == len(distinctStrings(sent))
}

// messagesOf is what a notification says, a message per language: each in a
// language the installation speaks and at most once, with a subject and a text
// within their bounds.
func messagesOf(sent []command.NotificationMessageCommand) ([]model.NotificationMessage, bool) {
	messages := make([]model.NotificationMessage, 0, len(sent))

	for _, message := range sent {
		language := strings.TrimSpace(message.Language)
		subject := strings.TrimSpace(message.Subject)
		body := strings.TrimSpace(message.Body)

		if !model.IsSupportedLanguage(language) ||
			slices.ContainsFunc(messages, func(m model.NotificationMessage) bool { return m.Language == language }) ||
			subject == "" || model.TooLong(subject, model.MaxTicketTitleLength) ||
			body == "" || model.TooLong(body, model.MaxNotificationBodyLength) {
			return nil, false
		}

		messages = append(messages, model.NotificationMessage{Language: language, Subject: subject, Body: body})
	}

	return messages, true
}

// distinctStrings is the trimmed values sent, each once.
func distinctStrings(sent []string) []string {
	seen := make([]string, 0, len(sent))

	for _, value := range sent {
		if trimmed := strings.TrimSpace(value); !slices.Contains(seen, trimmed) {
			seen = append(seen, trimmed)
		}
	}

	return seen
}
