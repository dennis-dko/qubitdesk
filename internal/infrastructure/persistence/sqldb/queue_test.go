package sqldb_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// The queue repository over the real tables: the shipped queue reads back with
// the agents' access; a queue saved with access reads back with it; a change
// replaces the access rather than adding to it; a queue's tickets and the queues
// under it are counted; and deleting one takes its access and the agents' wish to
// be told about it along, which the keys would otherwise refuse on PostgreSQL.
func TestTheQueueRepositoryKeepsAQueueAndItsAccessTogether(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	queues := sqldb.NewQueueRepository(db, sqldb.DialectSQLite)
	roles := sqldb.NewRoleRepository(db, sqldb.DialectSQLite)

	agent, err := roles.GetByName(ctx, model.RoleAgent)
	if err != nil {
		t.Fatalf("read the agent role: %v", err)
	}

	admin, err := roles.GetByName(ctx, model.RoleAdmin)
	if err != nil {
		t.Fatalf("read the admin role: %v", err)
	}

	shipped, err := queues.GetAll(ctx)
	if err != nil || len(shipped) != 1 {
		t.Fatalf("the shipped queues read as %d, %v; want the one", len(shipped), err)
	}

	desk := shipped[0]
	if desk.Name != "Service Desk" || len(desk.Access) != 1 ||
		desk.Access[0] != (model.QueueAccess{RoleID: agent.ID, Level: model.AccessWrite}) {
		t.Errorf("the shipped queue reads as %+v; want the Service Desk the agents work in", desk)
	}

	made, err := queues.Save(ctx, &model.Queue{
		Name: "Second level", ParentID: &desk.ID, FollowUp: model.FollowUpReject, UnlockMinutes: 30, Valid: true,
		Access: []model.QueueAccess{{RoleID: agent.ID, Level: model.AccessRead}, {RoleID: admin.ID, Level: model.AccessWrite}},
	})
	if err != nil {
		t.Fatalf("save a queue: %v", err)
	}

	if made.ParentID == nil || *made.ParentID != desk.ID || made.FollowUp != model.FollowUpReject ||
		made.UnlockMinutes != 30 || len(made.Access) != 2 {
		t.Errorf("the saved queue reads back as %+v", made)
	}

	made.Access = []model.QueueAccess{{RoleID: agent.ID, Level: model.AccessWrite}}

	changed, err := queues.Update(ctx, made)
	if err != nil || len(changed.Access) != 1 || changed.Access[0].Level != model.AccessWrite {
		t.Fatalf("replacing the access answered %+v, %v", changed, err)
	}

	if _, err := queues.Update(ctx, changed); err != nil {
		t.Errorf("saving the queue unchanged answered %v; MySQL counts no changed row there", err)
	}

	if children, err := queues.CountChildren(ctx, desk.ID); err != nil || children != 1 {
		t.Errorf("one queue sits under the desk and the count says %d, %v", children, err)
	}

	ticket(t, db, "2026111400000010", model.StateNew, nil, nil)

	if tickets, err := queues.CountTickets(ctx, desk.ID); err != nil || tickets != 1 {
		t.Errorf("one ticket is in the desk and the count says %d, %v", tickets, err)
	}

	leaving, _ := twoAccounts(t, db)

	if _, err := db.Exec("INSERT INTO user_queues (user_id, queue_id) VALUES (?, ?)", leaving.ID, made.ID); err != nil {
		t.Fatalf("ask to be told about the queue: %v", err)
	}

	if _, err := queues.Save(ctx, &model.Queue{Name: "Second level", FollowUp: model.FollowUpReopen}); err == nil {
		t.Error("a second queue with the same name was stored")
	} else if detail, ok := apperror.Detail(err); !ok || detail.Code != "queueNameTaken" {
		t.Errorf("a second queue with the same name answered %v, want queueNameTaken", err)
	}

	if err := queues.Delete(ctx, made.ID); err != nil {
		t.Fatalf("delete the queue: %v", err)
	}

	for _, table := range []string{"queue_roles", "user_queues"} {
		if left := count(t, db, "SELECT COUNT(*) FROM "+table+" WHERE queue_id = ?", made.ID); left != 0 {
			t.Errorf("%d row(s) of %s still point at the deleted queue", left, table)
		}
	}

	if _, err := queues.GetByID(ctx, made.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("reading the deleted queue answered %v, want not found", err)
	}
}
