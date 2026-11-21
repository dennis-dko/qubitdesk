package service_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// linkWorld is a ticket in the Service Desk, which the agent works in, one in a
// queue the agent does not reach, and a laptop among the items.
type linkWorld struct {
	links   *service.LinkApplicationService
	f       ticketFixture
	desk    *model.Ticket
	board   *model.Ticket
	laptop  uint
	another uint
}

func newLinkWorld(t *testing.T) linkWorld {
	t.Helper()

	ctx := context.Background()
	f := newTicketFixture(t)
	items := newItemWorld(t)

	laptop, err := items.items.Create(ctx, 1, items.computer, items.version("Erika's laptop", items.laptopData()))
	if err != nil {
		t.Fatalf("making the laptop: %v", err)
	}

	another, err := items.items.Create(ctx, 1, items.computer, items.version("Spare laptop", items.laptopData()))
	if err != nil {
		t.Fatalf("making the spare: %v", err)
	}

	return linkWorld{
		links: service.NewLinkApplicationService(memory.NewLinkRepository(), f.tickets, items.items),
		f:     f, desk: f.open(t, f.admin, f.desk, "Printer"), board: f.open(t, f.admin, f.hidden, "Board matter"),
		laptop: laptop.Item.ID, another: another.Item.ID,
	}
}

func (w linkWorld) link(caller *service.Principal, sourceObject model.LinkObject, sourceID uint,
	targetObject model.LinkObject, targetID uint, kind model.LinkType,
) (*service.LinkedRecord, error) {
	return w.links.Create(context.Background(), caller, service.LinkCommand{
		SourceObject: sourceObject, SourceID: sourceID, TargetObject: targetObject, TargetID: targetID, Type: kind,
	})
}

// An agent links a ticket they work on to an item they can read, and the link
// is seen from either end - outward from the ticket, inward from the item -
// each end named by its number and what it is called.
func TestALinkJoinsATicketAndAnItemSeenFromBothEnds(t *testing.T) {
	ctx := context.Background()
	w := newLinkWorld(t)

	made, err := w.link(w.f.agent, model.LinkTicket, w.desk.ID, model.LinkConfigItem, w.laptop, model.LinkDependsOn)
	if err != nil {
		t.Fatalf("the agent linking the printer's ticket to the laptop: %v", err)
	}

	if made.Object != model.LinkConfigItem || made.ID != w.laptop || !made.Outward || made.Label == "" {
		t.Errorf("the link was answered as %+v", made)
	}

	fromTicket, err := w.links.Of(ctx, w.f.agent, model.LinkTicket, w.desk.ID)
	if err != nil || len(fromTicket) != 1 || fromTicket[0].ID != w.laptop || !fromTicket[0].Outward {
		t.Errorf("from the ticket the links read %+v (%v)", fromTicket, err)
	}

	fromItem, err := w.links.Of(ctx, w.f.agent, model.LinkConfigItem, w.laptop)
	if err != nil || len(fromItem) != 1 || fromItem[0].ID != w.desk.ID || fromItem[0].Outward ||
		fromItem[0].Label != w.desk.Number+" Printer" {
		t.Errorf("from the item the links read %+v (%v)", fromItem, err)
	}
}

// A link to a record the caller may not see is left out of what they read,
// as the record itself is not found to them; and nobody links to it.
func TestALinkToWhatTheCallerMayNotSeeIsLeftOut(t *testing.T) {
	ctx := context.Background()
	w := newLinkWorld(t)

	if _, err := w.link(w.f.admin, model.LinkTicket, w.board.ID, model.LinkConfigItem, w.laptop,
		model.LinkNormal); err != nil {
		t.Fatalf("the administrator linking the board's ticket: %v", err)
	}

	seen, err := w.links.Of(ctx, w.f.agent, model.LinkConfigItem, w.laptop)
	if err != nil || len(seen) != 0 {
		t.Errorf("the agent sees %+v (%v) linked to the laptop, where the one link is to a queue they do not reach",
			seen, err)
	}

	_, err = w.link(w.f.agent, model.LinkTicket, w.desk.ID, model.LinkTicket, w.board.ID, model.LinkNormal)
	if apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("the agent linking to a ticket they do not reach was answered %v, want not found", err)
	}

	// Refused before anything is written: a link kept behind the refusal would
	// tell whoever reaches both tickets something the agent never could.
	if kept, _ := w.links.Of(ctx, w.f.admin, model.LinkTicket, w.desk.ID); len(kept) != 0 {
		t.Errorf("the refused link was kept all the same: %+v", kept)
	}

	if _, err := w.links.Of(ctx, w.f.agent, model.LinkTicket, w.board.ID); apperror.KindOf(err) !=
		apperror.KindNotFound {
		t.Errorf("the agent reading the links of a ticket they do not reach was answered %v, want not found", err)
	}
}

// Two records are joined once, whichever way round, and nothing is linked to
// itself or in a type there is not.
func TestARecordIsJoinedToAnotherOnce(t *testing.T) {
	w := newLinkWorld(t)

	if _, err := w.link(w.f.admin, model.LinkConfigItem, w.laptop, model.LinkConfigItem, w.another,
		model.LinkParentChild); err != nil {
		t.Fatalf("linking the two laptops: %v", err)
	}

	for name, refused := range map[string]struct {
		code string
		err  error
	}{
		"the same link again": {"linkTaken", second(w.link(w.f.admin, model.LinkConfigItem, w.laptop,
			model.LinkConfigItem, w.another, model.LinkNormal))},
		"the same link the other way round": {"linkTaken", second(w.link(w.f.admin, model.LinkConfigItem, w.another,
			model.LinkConfigItem, w.laptop, model.LinkDependsOn))},
		"a laptop to itself": {"linkToItself", second(w.link(w.f.admin, model.LinkConfigItem, w.laptop,
			model.LinkConfigItem, w.laptop, model.LinkNormal))},
	} {
		if detail, ok := apperror.Detail(refused.err); !ok || detail.Code != refused.code {
			t.Errorf("%s was answered %v, want %s", name, refused.err, refused.code)
		}
	}

	if _, err := w.link(w.f.admin, model.LinkTicket, w.desk.ID, model.LinkConfigItem, w.laptop,
		"RelatedSomehow"); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a type there is not was answered %v, want it refused", err)
	}
}

// A link is undone through an end the caller may change: the agent undoes it
// through their ticket, not through the item they only read, and not through a
// record that is not at either end.
func TestALinkIsUndoneThroughAnEndTheCallerMayChange(t *testing.T) {
	ctx := context.Background()
	w := newLinkWorld(t)

	made, err := w.link(w.f.agent, model.LinkTicket, w.desk.ID, model.LinkConfigItem, w.laptop, model.LinkNormal)
	if err != nil {
		t.Fatal(err)
	}

	id := made.Link.ID

	if err := w.links.Delete(ctx, w.f.agent, model.LinkConfigItem, w.laptop, id); apperror.KindOf(err) !=
		apperror.KindNotFound {
		t.Errorf("the agent undoing it through the item they only read was answered %v, want not found", err)
	}

	if err := w.links.Delete(ctx, w.f.admin, model.LinkConfigItem, w.another, id); apperror.KindOf(err) !=
		apperror.KindNotFound {
		t.Errorf("undoing it through an item at neither end was answered %v, want not found", err)
	}

	if err := w.links.Delete(ctx, w.f.agent, model.LinkTicket, w.desk.ID, id); err != nil {
		t.Fatalf("the agent undoing it through their ticket: %v", err)
	}

	if left, _ := w.links.Of(ctx, w.f.admin, model.LinkTicket, w.desk.ID); len(left) != 0 {
		t.Errorf("after undoing it the ticket still has %+v", left)
	}
}

// second is the error of a call answering a value and an error.
func second[T any](_ T, err error) error { return err }
