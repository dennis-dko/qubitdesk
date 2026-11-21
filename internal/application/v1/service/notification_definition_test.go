package service_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// newNotificationService is the notification service over in-memory tables
// holding one queue, and that queue's id.
func newNotificationService(t *testing.T) (*service.NotificationDefinitionApplicationService, uint) {
	t.Helper()

	queues := memory.NewQueueRepository()
	desk, err := queues.Save(context.Background(),
		&model.Queue{Name: "Service Desk", Valid: true, FollowUp: model.FollowUpReopen})
	if err != nil {
		t.Fatal(err)
	}

	refs := service.MacroReferences{
		Catalog: service.TicketCatalogRepositories{
			States: memory.NewTicketStateRepository(), Priorities: memory.NewTicketPriorityRepository(),
			Types: memory.NewTicketTypeRepository(),
		},
		Queues: queues,
	}

	return service.NewNotificationDefinitionApplicationService(memory.NewNotificationDefinitionRepository(), refs), desk.ID
}

// english is a message in English that every notification below can say.
func english() *[]command.NotificationMessageCommand {
	return &[]command.NotificationMessageCommand{{Language: "en", Subject: "New", Body: "A ticket was opened."}}
}

// A notification is kept with what it names, each once and in the order a form
// offers them, and with what it says in each language, trimmed; one about every
// ticket is kept too, because telling somebody about a ticket changes nothing in
// it. A change that sends only a name leaves the rest as it was, and a list sent
// takes the place of the one before.
func TestANotificationIsKeptWithWhatItSays(t *testing.T) {
	ctx := context.Background()
	notifications, desk := newNotificationService(t)

	made, err := notifications.Create(ctx, command.NotificationDefinitionCommand{
		Name:       ptr(" Opened at the desk "),
		Events:     &[]string{"ArticleCreate", "TicketCreate", "TicketCreate"},
		Criteria:   &command.JobCriteriaCommand{QueueIDs: []uint{desk, desk}},
		Recipients: &[]string{"AgentWritePermissions", "AgentOwner", "AgentOwner"},
		OncePerDay: ptr(true),
		Optional:   ptr(true),
		Messages: &[]command.NotificationMessageCommand{
			{Language: "de", Subject: " Neu ", Body: " Ein Ticket wurde eröffnet. "},
			{Language: "en", Subject: "New", Body: "A ticket was opened."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	wantEvents := []model.TicketEvent{model.EventTicketCreate, model.EventArticleCreate}
	wantRecipients := []model.NotificationRecipient{model.RecipientOwner, model.RecipientWriters}

	if made.Name != "Opened at the desk" || !made.Valid || !made.OncePerDay || !made.Optional ||
		!slices.Equal(made.Events, wantEvents) || !slices.Equal(made.Recipients, wantRecipients) ||
		!slices.Equal(made.Criteria.QueueIDs, []uint{desk}) || len(made.Messages) != 2 ||
		made.Messages[0] != (model.NotificationMessage{Language: "de", Subject: "Neu", Body: "Ein Ticket wurde eröffnet."}) {
		t.Fatalf("the notification was kept as %+v", made)
	}

	renamed, err := notifications.Update(ctx, made.ID, command.NotificationDefinitionCommand{Name: ptr("Opened")})
	if err != nil || len(renamed.Events) != 2 || len(renamed.Recipients) != 2 || len(renamed.Messages) != 2 ||
		len(renamed.Criteria.QueueIDs) != 1 {
		t.Errorf("a change sending only a name left the notification as %+v, %v", renamed, err)
	}

	shorter, err := notifications.Update(ctx, made.ID, command.NotificationDefinitionCommand{Messages: english()})
	if err != nil || len(shorter.Messages) != 1 || shorter.Messages[0].Language != "en" {
		t.Errorf("a change sending one message left the notification saying %+v, %v", shorter.Messages, err)
	}

	everything, err := notifications.Create(ctx, command.NotificationDefinitionCommand{
		Name: ptr("Anything"), Events: &[]string{"TicketCreate"}, Criteria: &command.JobCriteriaCommand{},
		Recipients: &[]string{"AgentWatcher"}, Messages: english(),
	})
	if err != nil || !everything.Criteria.Empty() {
		t.Errorf("a notification about every ticket was answered %+v, %v", everything, err)
	}
}

// A notification that could not be sent as it says is refused, with every field
// at fault: no name, event, recipient or message; an event, a recipient or a
// record its criteria name that is not there; a message in a language the
// installation does not speak, a language twice, or a subject or text that is
// empty or too long.
func TestANotificationThatCouldNotBeSentIsRefused(t *testing.T) {
	ctx := context.Background()
	notifications, _ := newNotificationService(t)

	opened := &[]string{"TicketCreate"}
	owner := &[]string{"AgentOwner"}
	message := func(language, subject, body string) *[]command.NotificationMessageCommand {
		return &[]command.NotificationMessageCommand{{Language: language, Subject: subject, Body: body}}
	}
	sent := func(change func(*command.NotificationDefinitionCommand)) command.NotificationDefinitionCommand {
		cmd := command.NotificationDefinitionCommand{
			Name: ptr("x"), Events: opened, Recipients: owner, Messages: english(),
		}
		change(&cmd)

		return cmd
	}

	for name, refused := range map[string]struct {
		sent   command.NotificationDefinitionCommand
		fields []string
	}{
		"nothing":               {command.NotificationDefinitionCommand{}, []string{"name", "events", "recipients", "messages"}},
		"no event":              {sent(func(c *command.NotificationDefinitionCommand) { c.Events = &[]string{} }), []string{"events"}},
		"an event there is not": {sent(func(c *command.NotificationDefinitionCommand) { c.Events = &[]string{"TicketCreate", "Nonsense"} }), []string{"events"}},
		"a queue there is not": {sent(func(c *command.NotificationDefinitionCommand) {
			c.Criteria = &command.JobCriteriaCommand{QueueIDs: []uint{99}}
		}), []string{"criteria"}},
		"no recipient":             {sent(func(c *command.NotificationDefinitionCommand) { c.Recipients = &[]string{} }), []string{"recipients"}},
		"a recipient there is not": {sent(func(c *command.NotificationDefinitionCommand) { c.Recipients = &[]string{"Everybody"} }), []string{"recipients"}},
		"no message":               {sent(func(c *command.NotificationDefinitionCommand) { c.Messages = &[]command.NotificationMessageCommand{} }), []string{"messages"}},
		"a language not spoken":    {sent(func(c *command.NotificationDefinitionCommand) { c.Messages = message("fr", "Nouveau", "Un ticket.") }), []string{"messages"}},
		"an empty subject":         {sent(func(c *command.NotificationDefinitionCommand) { c.Messages = message("en", " ", "A ticket.") }), []string{"messages"}},
		"an empty text":            {sent(func(c *command.NotificationDefinitionCommand) { c.Messages = message("en", "New", "") }), []string{"messages"}},
		"a subject too long": {sent(func(c *command.NotificationDefinitionCommand) {
			c.Messages = message("en", strings.Repeat("s", model.MaxTicketTitleLength+1), "A ticket.")
		}), []string{"messages"}},
		"a text too long": {sent(func(c *command.NotificationDefinitionCommand) {
			c.Messages = message("en", "New", strings.Repeat("b", model.MaxNotificationBodyLength+1))
		}), []string{"messages"}},
		"a long name, no recipient": {sent(func(c *command.NotificationDefinitionCommand) {
			c.Name = ptr(strings.Repeat("n", model.MaxCatalogNameLength+1))
			c.Recipients = &[]string{}
		}), []string{"name", "recipients"}},
		"a language twice": {sent(func(c *command.NotificationDefinitionCommand) {
			c.Messages = &[]command.NotificationMessageCommand{
				{Language: "en", Subject: "New", Body: "A ticket."}, {Language: "en", Subject: "Again", Body: "A ticket."},
			}
		}), []string{"messages"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := notifications.Create(ctx, refused.sent)

			detail, ok := apperror.Detail(err)
			if !ok || strings.Join(detail.Fields, ",") != strings.Join(refused.fields, ",") {
				t.Errorf("the notification was answered %v; want %v refused", err, refused.fields)
			}
		})
	}
}

// A second notification of a name taken is refused with the name, so that the
// refusal can say which.
func TestANotificationsNameIsItsOwn(t *testing.T) {
	ctx := context.Background()
	notifications, _ := newNotificationService(t)

	made := command.NotificationDefinitionCommand{
		Name: ptr("Opened"), Events: &[]string{"TicketCreate"}, Recipients: &[]string{"AgentOwner"}, Messages: english(),
	}

	if _, err := notifications.Create(ctx, made); err != nil {
		t.Fatal(err)
	}

	_, err := notifications.Create(ctx, made)
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "notificationNameTaken" ||
		len(detail.Values) != 1 || detail.Values[0] != "Opened" {
		t.Errorf("a second notification named Opened was answered %v", err)
	}
}
