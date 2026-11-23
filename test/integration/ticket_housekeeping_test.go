//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// The application keeps house by itself, as it is shipped and on the suite's
// database: a locked ticket whose wait is over is closed and freed, and a lock
// that has run as long as its queue allows is freed, each written in the
// history by the application. Its scheduler looks once a minute and a queue
// frees a lock after a minute at the least, so this waits up to two.
func TestTheApplicationEndsWaitsAndFreesLocksByItself(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	admin.must(admin.api(http.MethodPut, "/queues/"+strconv.FormatUint(uint64(desk), 10),
		map[string]any{"unlockMinutes": 1}), http.StatusOK)

	var states catalogList

	admin.must(admin.api(http.MethodGet, "/ticket-states", nil), http.StatusOK).Data(t, &states)

	named := map[string]uint{}
	for _, state := range states.Items {
		named[state.Name] = state.ID
	}

	waiting := openTicket(t, admin, desk, "Printer", nil)
	admin.must(admin.api(http.MethodPut, ticketPath(waiting.ID), map[string]any{
		"stateId": named["pending-auto-close"], "pendingUntil": time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339),
		"locked": true,
	}), http.StatusOK)

	held := openTicket(t, admin, desk, "Scanner", nil)
	admin.must(admin.api(http.MethodPut, ticketPath(held.ID), map[string]any{"locked": true}), http.StatusOK)

	read := func(id uint) ticketResponse {
		var ticket ticketResponse

		admin.must(admin.api(http.MethodGet, ticketPath(id), nil), http.StatusOK).Data(t, &ticket)

		return ticket
	}

	deadline := time.Now().Add(150 * time.Second)

	for {
		printer, scanner := read(waiting.ID), read(held.ID)
		if printer.StateID == named["closed"] && !printer.Locked && !scanner.Locked {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("after two and a half minutes the printer reads %+v and the scanner %+v; "+
				"want the printer closed and both free", printer, scanner)
		}

		time.Sleep(2 * time.Second)
	}

	for _, id := range []uint{waiting.ID, held.ID} {
		var history struct {
			Items []struct {
				Actor    string `json:"actor"`
				Event    string `json:"event"`
				NewValue string `json:"newValue"`
			} `json:"items"`
		}

		admin.must(admin.api(http.MethodGet, ticketPath(id, "history"), nil), http.StatusOK).Data(t, &history)

		last := history.Items[len(history.Items)-1]
		if last.Actor != "system" || last.Event != "lock" || last.NewValue != "false" {
			t.Errorf("ticket %d's history ends with %+v; want the application freeing it", id, last)
		}
	}
}
