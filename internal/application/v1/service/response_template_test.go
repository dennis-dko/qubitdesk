package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// templating is the ticket fixture with the answer templates over it.
type templating struct {
	ticketFixture
	templates *service.ResponseTemplateApplicationService
	stored    *memory.ResponseTemplateRepository
}

func newTemplating(t *testing.T) templating {
	t.Helper()

	f := newTicketFixture(t)
	stored := memory.NewResponseTemplateRepository()
	refs := service.MacroReferences{Catalog: f.catalog, Customers: f.customers, Queues: f.queues, Users: f.users}

	return templating{
		ticketFixture: f, stored: stored,
		templates: service.NewResponseTemplateApplicationService(stored, f.tickets, refs),
	}
}

// make keeps a template, and fails the case if it cannot.
func (f templating) make(t *testing.T, cmd command.ResponseTemplateCommand) *model.ResponseTemplate {
	t.Helper()

	made, err := f.templates.Create(context.Background(), cmd)
	if err != nil {
		t.Fatalf("make the template %s: %v", *cmd.Name, err)
	}

	return made
}

// A template is offered for a ticket in the queue it names, or in every queue
// when it names none, while it is valid - and comes with the ticket's
// placeholders filled in the reader's words.
func TestAnAnswerTemplateIsOfferedWhereItSays(t *testing.T) {
	ctx := context.Background()
	f := newTemplating(t)

	f.make(t, command.ResponseTemplateCommand{Name: ptr("Thanks"), Subject: ptr("Re: <KIX_TICKET_Title>"),
		Body: ptr("Thank you. It is <KIX_TICKET_State> now.")})
	f.make(t, command.ResponseTemplateCommand{Name: ptr("Desk"), Body: ptr("The desk has it."), QueueID: &f.desk.ID})
	f.make(t, command.ResponseTemplateCommand{Name: ptr("Finance"), Body: ptr("Finance has it."),
		QueueID: &f.readOnly.ID})
	f.make(t, command.ResponseTemplateCommand{Name: ptr("Old"), Body: ptr("Retired."), Valid: ptr(false)})

	printer := f.open(t, f.agent, f.desk, "Printer")

	offered, err := f.templates.ForTicket(ctx, f.agent, printer.ID)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(offered))
	for _, template := range offered {
		names = append(names, template.Name)
	}

	if strings.Join(names, ",") != "Desk,Thanks" {
		t.Fatalf("a desk ticket is offered %v; want the desk's and the one for every queue", names)
	}

	if offered[1].Subject != "Re: Printer" || offered[1].Body != "Thank you. It is new now." {
		t.Errorf("the template for every queue reads %q, %q", offered[1].Subject, offered[1].Body)
	}

	german := *f.agentUser
	german.Language = model.LanguageGerman
	reader := &service.Principal{User: &german, Role: f.agent.Role, Permissions: f.agent.Permissions}

	if offered, _ := f.templates.ForTicket(ctx, reader, printer.ID); offered[1].Body != "Thank you. It is neu now." {
		t.Errorf("for a German reader the state reads %q", offered[1].Body)
	}
}

// A ticket the caller may not read offers them nothing, because it is not found.
func TestATicketTheCallerMayNotReadOffersNoTemplates(t *testing.T) {
	ctx := context.Background()
	f := newTemplating(t)
	board := f.open(t, f.admin, f.hidden, "Board meeting")

	if _, err := f.templates.ForTicket(ctx, f.agent, board.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("an agent asking for the templates of a ticket they do not reach was answered %v", err)
	}
}

// A template that could not be kept is refused, with every field at fault - no
// name or text, a text too long, a queue there is not - and a second of its
// name with the name.
func TestAnAnswerTemplateThatCouldNotBeKeptIsRefused(t *testing.T) {
	ctx := context.Background()
	f := newTemplating(t)
	nowhere := uint(99)

	for name, refused := range map[string]struct {
		sent   command.ResponseTemplateCommand
		fields []string
	}{
		"nothing":            {command.ResponseTemplateCommand{}, []string{"name", "body"}},
		"an empty text":      {command.ResponseTemplateCommand{Name: ptr("x"), Body: ptr(" ")}, []string{"body"}},
		"a text too long":    {command.ResponseTemplateCommand{Name: ptr("x"), Body: ptr(strings.Repeat("b", model.MaxTemplateBodyLength+1))}, []string{"body"}},
		"a subject too long": {command.ResponseTemplateCommand{Name: ptr("x"), Body: ptr("b"), Subject: ptr(strings.Repeat("s", model.MaxTicketTitleLength+1))}, []string{"subject"}},
		"a queue not there":  {command.ResponseTemplateCommand{Name: ptr("x"), Body: ptr("b"), QueueID: &nowhere}, []string{"queueId"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.templates.Create(ctx, refused.sent)

			detail, ok := apperror.Detail(err)
			if !ok || strings.Join(detail.Fields, ",") != strings.Join(refused.fields, ",") {
				t.Errorf("the template was answered %v; want %v refused", err, refused.fields)
			}
		})
	}

	f.make(t, command.ResponseTemplateCommand{Name: ptr("Thanks"), Body: ptr("Thank you.")})

	_, err := f.templates.Create(ctx, command.ResponseTemplateCommand{Name: ptr("Thanks"), Body: ptr("Again.")})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "templateNameTaken" {
		t.Errorf("a second template named Thanks was answered %v", err)
	}
}

// A change that clears the queue offers the template in every queue again,
// where one that sends no queue leaves it where it was.
func TestAnAnswerTemplatesQueueIsClearedOnlyWhenSaid(t *testing.T) {
	ctx := context.Background()
	f := newTemplating(t)
	made := f.make(t, command.ResponseTemplateCommand{Name: ptr("Desk"), Body: ptr("b"), QueueID: &f.desk.ID})

	kept, err := f.templates.Update(ctx, made.ID, command.ResponseTemplateCommand{Name: ptr("Desk only")})
	if err != nil || kept.QueueID == nil || *kept.QueueID != f.desk.ID {
		t.Fatalf("a change sending no queue left the template as %+v, %v", kept, err)
	}

	cleared, err := f.templates.Update(ctx, made.ID, command.ResponseTemplateCommand{ClearQueue: true})
	if err != nil || cleared.QueueID != nil {
		t.Errorf("clearing the queue left the template as %+v, %v", cleared, err)
	}
}

// A queue a template is offered in alone is not deleted from under it, and the
// refusal says how many; once the template is gone the queue goes.
func TestAQueueATemplateNamesIsNotDeleted(t *testing.T) {
	ctx := context.Background()
	f := newTemplating(t)
	queues := service.NewQueueApplicationService(f.queues, f.roles, f.levels).WithTemplates(f.stored)
	made := f.make(t, command.ResponseTemplateCommand{Name: ptr("Finance"), Body: ptr("b"), QueueID: &f.readOnly.ID})

	err := queues.DeleteQueue(ctx, f.readOnly.ID)
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "queueHasTemplates" || len(detail.Values) != 2 ||
		detail.Values[1] != 1 {
		t.Fatalf("deleting a queue a template names was answered %v", err)
	}

	if err := f.templates.Delete(ctx, made.ID); err != nil {
		t.Fatal(err)
	}

	if err := queues.DeleteQueue(ctx, f.readOnly.ID); err != nil {
		t.Errorf("a queue no template names any more was refused: %v", err)
	}
}
