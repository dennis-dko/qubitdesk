package sqldb_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
)

// What an agent was told leaves for the browsers once: the unsent ones are
// handed out the oldest first, and once marked sent they are not handed out
// again, while what was told stays for the agent to read.
func TestANotificationLeavesOnce(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	agent, _ := twoAccounts(t, db)
	printer := ticket(t, db, "2026111700000001", model.StateNew, nil, nil)
	sent := sqldb.NewNotificationRepository(db, sqldb.DialectSQLite)
	now := time.Now().UTC()

	if err := sent.Add(ctx, []model.Notification{
		{UserID: agent.ID, TicketID: printer, DefinitionID: 1, Subject: "New", Body: "Opened.", CreatedAt: now},
		{UserID: agent.ID, TicketID: printer, DefinitionID: 1, Subject: "Again", Body: "Changed.", CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}

	unsent, err := sent.Unsent(ctx, 10)
	if err != nil || len(unsent) != 2 || unsent[0].Subject != "New" {
		t.Fatalf("the unsent read %+v, %v; want both, the oldest first", unsent, err)
	}

	if err := sent.MarkSent(ctx, []uint{unsent[0].ID, unsent[1].ID}); err != nil {
		t.Fatal(err)
	}

	if again, err := sent.Unsent(ctx, 10); err != nil || len(again) != 0 {
		t.Errorf("after both left the unsent read %+v, %v", again, err)
	}

	if mine, unread, err := sent.ForUser(ctx, agent.ID, 10); err != nil || len(mine) != 2 || unread != 2 {
		t.Errorf("the agent reads %d, %d unread, %v; want both still there to read", len(mine), unread, err)
	}
}
