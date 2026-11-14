package service_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// queueFixture is the queue service over an empty queue table and the shipped
// roles, with a caller for each side of queues:write.
type queueFixture struct {
	queues  *service.QueueApplicationService
	repo    *memory.QueueRepository
	agent   *model.Role
	admin   *service.Principal
	working *service.Principal
}

func newQueueFixture(t *testing.T) queueFixture {
	t.Helper()

	roles := memory.NewRoleRepository(nil)

	agent, err := roles.GetByName(context.Background(), model.RoleAgent)
	if err != nil {
		t.Fatalf("read the agent role: %v", err)
	}

	repo := memory.NewQueueRepository()

	return queueFixture{
		queues: service.NewQueueApplicationService(repo, roles),
		repo:   repo,
		agent:  agent,
		admin: &service.Principal{
			User: &model.User{ID: 1}, Permissions: model.AdminPermissions(),
		},
		working: &service.Principal{
			User: &model.User{ID: 2}, Role: agent, Permissions: model.AgentPermissions(),
		},
	}
}

func (f queueFixture) create(t *testing.T, cmd command.QueueCommand) *model.Queue {
	t.Helper()

	queue, err := f.queues.CreateQueue(context.Background(), cmd)
	if err != nil {
		t.Fatalf("create the queue %v: %v", cmd.Name, err)
	}

	return queue
}

func access(roleID uint, level model.AccessLevel) *[]command.QueueAccessCommand {
	return &[]command.QueueAccessCommand{{RoleID: roleID, Level: string(level)}}
}

// A queue is there for the roles it reaches and for whoever holds queues:write,
// and for nobody else - not refused, not found, because the difference is itself
// what the caller may not learn. Whoever it reaches is shown their own line of its
// access, and not who else reaches it.
func TestAQueueIsSeenOnlyByTheRolesItReaches(t *testing.T) {
	ctx := context.Background()
	f := newQueueFixture(t)

	desk := f.create(t, command.QueueCommand{Name: ptr("Service Desk"), Access: access(f.agent.ID, model.AccessWrite)})
	hidden := f.create(t, command.QueueCommand{Name: ptr("Second level")})

	seen, err := f.queues.ListQueues(ctx, f.working)
	if err != nil {
		t.Fatalf("list as an agent: %v", err)
	}

	if len(seen) != 1 || seen[0].ID != desk.ID {
		t.Fatalf("an agent sees %d queue(s), want only the one their role reaches", len(seen))
	}

	if level, ok := service.Reach(f.working, seen[0]); !ok || level != model.AccessWrite {
		t.Errorf("the agent reaches the desk as %q, %v; want write", level, ok)
	}

	_, err = f.queues.GetQueue(ctx, f.working, hidden.ID)
	if apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("reading a queue the role does not reach answered %v, want not found", err)
	}

	all, err := f.queues.ListQueues(ctx, f.admin)
	if err != nil || len(all) != 2 {
		t.Fatalf("whoever holds queues:write sees %d queue(s), %v; want both", len(all), err)
	}

	f.create(t, command.QueueCommand{Name: ptr("Shared"), Access: &[]command.QueueAccessCommand{
		{RoleID: f.agent.ID, Level: string(model.AccessRead)},
		{RoleID: 1, Level: string(model.AccessWrite)},
	}})

	seen, _ = f.queues.ListQueues(ctx, f.working)
	for _, queue := range seen {
		if len(queue.Access) != 1 || queue.Access[0].RoleID != f.agent.ID {
			t.Errorf("the agent is shown %+v of %s's access, want only their own line", queue.Access, queue.Name)
		}
	}
}

// A queue sits under another for finding it, so a parent that would put it under
// itself - directly or two levels down - is refused, and so is one that is not
// there.
func TestAQueueCannotSitUnderItself(t *testing.T) {
	ctx := context.Background()
	f := newQueueFixture(t)

	top := f.create(t, command.QueueCommand{Name: ptr("Top")})
	middle := f.create(t, command.QueueCommand{Name: ptr("Middle"), ParentID: &top.ID})
	bottom := f.create(t, command.QueueCommand{Name: ptr("Bottom"), ParentID: &middle.ID})

	for _, parent := range []uint{top.ID, bottom.ID} {
		_, err := f.queues.UpdateQueue(ctx, top.ID, command.QueueCommand{ParentID: &parent})
		refusedWith(t, err, "queueUnderItself")
	}

	missing := uint(999)
	_, err := f.queues.UpdateQueue(ctx, bottom.ID, command.QueueCommand{ParentID: &missing})
	invalidFields(t, err, "parentId")

	_, err = f.queues.UpdateQueue(ctx, bottom.ID, command.QueueCommand{ParentID: &top.ID, ClearParent: true})
	invalidFields(t, err, "parentId")

	moved, err := f.queues.UpdateQueue(ctx, bottom.ID, command.QueueCommand{ClearParent: true})
	if err != nil || moved.ParentID != nil {
		t.Fatalf("moving a queue to the top answered %+v, %v", moved, err)
	}
}

// A queue holding tickets is made invalid rather than deleted, and the refusal
// says how many it holds; one other queues sit under is refused too.
func TestAQueueHoldingTicketsOrQueuesIsNotDeleted(t *testing.T) {
	ctx := context.Background()
	f := newQueueFixture(t)

	busy := f.create(t, command.QueueCommand{Name: ptr("Busy")})
	parent := f.create(t, command.QueueCommand{Name: ptr("Parent")})
	f.create(t, command.QueueCommand{Name: ptr("Child"), ParentID: &parent.ID})

	f.repo.UseInTickets(busy.ID, 4)

	detail := refusal(t, f.queues.DeleteQueue(ctx, busy.ID), "queueInUse")
	if !slices.Equal(detail.Values, []any{"Busy", 4}) {
		t.Errorf("the refusal says %v, want the queue and how many tickets are in it", detail.Values)
	}

	refusedWith(t, f.queues.DeleteQueue(ctx, parent.ID), "queueHasChildren")

	f.repo.UseInTickets(busy.ID, 0)

	if err := f.queues.DeleteQueue(ctx, busy.ID); err != nil {
		t.Fatalf("deleting an empty queue: %v", err)
	}
}

// A queue's access names roles that exist, each once, at a level the code knows;
// anything else is refused as the access field rather than stored half.
func TestAQueuesAccessNamesRolesThatExistOnce(t *testing.T) {
	ctx := context.Background()
	f := newQueueFixture(t)

	for name, entries := range map[string][]command.QueueAccessCommand{
		"an unknown role":  {{RoleID: 999, Level: "read"}},
		"no role":          {{RoleID: 0, Level: "read"}},
		"a role twice":     {{RoleID: f.agent.ID, Level: "read"}, {RoleID: f.agent.ID, Level: "write"}},
		"an unknown level": {{RoleID: f.agent.ID, Level: "admin"}},
	} {
		_, err := f.queues.CreateQueue(ctx, command.QueueCommand{Name: ptr(name), Access: &entries})
		if detail, ok := apperror.Detail(err); !ok || !slices.Equal(detail.Fields, []string{"access"}) {
			t.Errorf("a queue with %s answered %v, want the access refused", name, err)
		}
	}
}

// Every field is checked before anything is refused, so a form learns everything
// wrong with it at once.
func TestAQueueIsCheckedWhole(t *testing.T) {
	f := newQueueFixture(t)

	_, err := f.queues.CreateQueue(context.Background(), command.QueueCommand{
		Name: ptr("  "), FollowUp: ptr("ignore"), UnlockMinutes: ptr(model.MaxUnlockMinutes + 1),
	})
	invalidFields(t, err, "name", "followUp", "unlockMinutes")

	queue := f.create(t, command.QueueCommand{Name: ptr("Defaults")})
	if queue.FollowUp != model.FollowUpReopen || queue.UnlockMinutes != 0 || !queue.Valid {
		t.Errorf("a new queue starts as %+v; want an answer to reopen, never unlocked, and valid", queue)
	}
}

// A change says what it changes: retiring a queue keeps its access, and access
// sent replaces the old rather than adding to it.
func TestAChangeToAQueueLeavesWhatItDoesNotMention(t *testing.T) {
	ctx := context.Background()
	f := newQueueFixture(t)

	queue := f.create(t, command.QueueCommand{Name: ptr("Desk"), Access: access(f.agent.ID, model.AccessWrite)})

	retired, err := f.queues.UpdateQueue(ctx, queue.ID, command.QueueCommand{Valid: ptr(false)})
	if err != nil || retired.Valid || len(retired.Access) != 1 {
		t.Fatalf("retiring the queue answered %+v, %v; want it invalid with its access kept", retired, err)
	}

	replaced, err := f.queues.UpdateQueue(ctx, queue.ID, command.QueueCommand{Access: access(f.agent.ID, model.AccessRead)})
	if err != nil || len(replaced.Access) != 1 || replaced.Access[0].Level != model.AccessRead {
		t.Fatalf("replacing the access answered %+v, %v; want the agent reading and nothing else", replaced, err)
	}
}

// A tree somebody broke by hand in the database ends the walk up it rather than
// the request: placing a queue under a loop the API would never have stored is
// refused, and refused in time.
func TestABrokenQueueTreeEndsTheWalk(t *testing.T) {
	ctx := context.Background()
	f := newQueueFixture(t)

	first := f.create(t, command.QueueCommand{Name: ptr("First")})
	second := f.create(t, command.QueueCommand{Name: ptr("Second"), ParentID: &first.ID})

	broken := *first
	broken.ParentID = &second.ID

	if _, err := f.repo.Update(ctx, &broken); err != nil {
		t.Fatalf("break the tree: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		_, err := f.queues.CreateQueue(ctx, command.QueueCommand{Name: ptr("Third"), ParentID: &first.ID})
		done <- err
	}()

	select {
	case err := <-done:
		refusedWith(t, err, "queueUnderItself")
	case <-time.After(5 * time.Second):
		t.Fatal("placing a queue under a broken tree never ended")
	}
}
