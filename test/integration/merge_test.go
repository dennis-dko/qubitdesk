//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// A ticket is merged into another over HTTP on every database: the other holds
// both tickets' articles and is linked to the merged one as its parent, the
// merged ticket names it and stands merged, and neither is merged again.
func TestATicketIsMergedIntoAnotherOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, _ := startWithWorker(t)
	desk := theDesk(t, admin)

	open := func(title string) ticketResponse {
		var ticket ticketResponse

		admin.must(admin.api(http.MethodPost, "/tickets", map[string]any{"title": title, "queueId": desk,
			"body": title + "."}), http.StatusCreated).Data(t, &ticket)

		return ticket
	}

	printer, again := open("Printer jams"), open("Printer jams again")

	if refused := admin.api(http.MethodPost, path("/tickets/", printer.ID)+"/merge",
		map[string]any{"intoId": printer.ID}); refused.Status != http.StatusBadRequest {
		t.Errorf("a ticket merged into itself was answered %d: %s", refused.Status, refused.Body)
	}

	var merged struct {
		StateID    uint  `json:"stateId"`
		MergedInto *uint `json:"mergedInto"`
	}

	admin.must(admin.api(http.MethodPost, path("/tickets/", again.ID)+"/merge",
		map[string]any{"intoId": printer.ID}), http.StatusCreated, http.StatusOK).Data(t, &merged)

	if merged.MergedInto == nil || *merged.MergedInto != printer.ID || merged.StateID == again.StateID {
		t.Errorf("the merged ticket reads %+v; want it merged into the printer's", merged)
	}

	var articles struct {
		Items []struct {
			Body string `json:"body"`
		} `json:"items"`
	}

	admin.must(admin.api(http.MethodGet, path("/tickets/", printer.ID)+"/articles", nil), http.StatusOK).
		Data(t, &articles)

	if len(articles.Items) != 2 {
		t.Errorf("the printer's ticket holds %+v after the merge, want both tickets' articles", articles.Items)
	}

	var linked listOf[linkedRecord]

	admin.must(admin.api(http.MethodGet, "/links?object=ticket&record="+path(printer.ID), nil), http.StatusOK).
		Data(t, &linked)

	if len(linked.Items) != 1 || linked.Items[0].ObjectID != again.ID || linked.Items[0].Type != "ParentChild" ||
		!linked.Items[0].Outward {
		t.Errorf("the printer's ticket is linked to %+v, want the merged ticket as its child", linked.Items)
	}

	if refused := admin.api(http.MethodPost, path("/tickets/", again.ID)+"/merge",
		map[string]any{"intoId": printer.ID}); refused.Status != http.StatusConflict ||
		refusalOf(t, refused).Code != "ticketMergedAlready" {
		t.Errorf("merging the merged ticket again was answered %d: %s", refused.Status, refused.Body)
	}
}
