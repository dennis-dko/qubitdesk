package sqldb_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// The customer repositories over the real tables: a number and an address are
// unique where given and absent for any number of rows, which only NULL makes
// true; what belongs to an organisation is counted; and deleting a contact keeps
// the tickets for it and takes the contact off them.
func TestTheCustomerTablesKeepTheirRules(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	organisations := sqldb.NewOrganisationRepository(db, sqldb.DialectSQLite)
	contacts := sqldb.NewContactRepository(db, sqldb.DialectSQLite)
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

	acme, err := organisations.Save(ctx, &model.Organisation{Name: "Acme", Number: "K-100", Valid: true, CreatedAt: now})
	if err != nil {
		t.Fatalf("save Acme: %v", err)
	}

	for _, name := range []string{"Bolt", "Cogs"} {
		if _, err := organisations.Save(ctx, &model.Organisation{Name: name, Valid: true, CreatedAt: now}); err != nil {
			t.Fatalf("an organisation with no number, %s, was refused: %v", name, err)
		}
	}

	_, err = organisations.Save(ctx, &model.Organisation{Name: "Acme again", Number: "K-100", Valid: true, CreatedAt: now})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "organisationNumberTaken" {
		t.Errorf("a second organisation numbered K-100 answered %v, want organisationNumberTaken", err)
	}

	ann, err := contacts.Save(ctx, &model.Contact{
		Name: "Ann", Email: "ann@example.com", OrganisationID: &acme.ID, Valid: true, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("save Ann: %v", err)
	}

	if _, err := contacts.Save(ctx, &model.Contact{Name: "Bob", Valid: true, CreatedAt: now}); err != nil {
		t.Fatalf("a contact with no address was refused: %v", err)
	}

	if _, err := contacts.Save(ctx, &model.Contact{Name: "Carl", Valid: true, CreatedAt: now}); err != nil {
		t.Errorf("a second contact with no address was refused: %v", err)
	}

	ticketID := ticket(t, db, "2026111400000020", model.StateOpen, nil, nil)
	if _, err := db.Exec("UPDATE tickets SET contact_id = ?, organisation_id = ? WHERE id = ?", ann.ID, acme.ID, ticketID); err != nil {
		t.Fatalf("give the ticket to Ann: %v", err)
	}

	if people, tickets, err := organisations.CountUses(ctx, acme.ID); err != nil || people != 1 || tickets != 1 {
		t.Errorf("Acme has %d contact(s) and %d ticket(s), %v; want one of each", people, tickets, err)
	}

	if found, total, _ := contacts.List(ctx, repository.CustomerFilter{Search: "ANN@", Limit: 10}); total != 1 ||
		found[0].ID != ann.ID {
		t.Errorf("searching for her address found %d", total)
	}

	if err := contacts.Delete(ctx, ann.ID); err != nil {
		t.Fatalf("delete Ann: %v", err)
	}

	var contactID *uint

	if err := db.QueryRow("SELECT contact_id FROM tickets WHERE id = ?", ticketID).Scan(&contactID); err != nil {
		t.Fatalf("read the ticket: %v", err)
	}

	if contactID != nil {
		t.Errorf("Ann's ticket still names contact %d after she was deleted", *contactID)
	}

	if _, err := contacts.GetByID(ctx, ann.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("reading the deleted contact answered %v, want not found", err)
	}
}
