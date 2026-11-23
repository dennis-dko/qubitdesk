//go:build browser

package browser

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// The ticket setup screen makes a state, corrects it and deletes it through the
// one form its card has, and shows a shipped state the code relies on without
// offering what the server refuses.
func TestTheTicketSetupScreenMakesCorrectsAndDeletesAState(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the ticket setup", p.click(`.tab[data-view="ticketsetup"]`),
		chromedp.WaitVisible("#table-state", chromedp.ByID))
	p.waitForText("#table-state tbody", "pending reminder")

	// A shipped state the code moves tickets into is shown and nothing more.
	var closedActions string

	p.evalJSON(`JSON.stringify([...document.querySelectorAll('#table-state tbody tr')]
		.filter((row) => [...row.cells].some((cell) => cell.textContent === 'closed'))
		.map((row) => row.querySelectorAll('button').length + ' ' + row.lastElementChild.textContent)
		.join('|'))`, &closedActions)

	if !strings.HasPrefix(closedActions, "0 ") {
		var rows string

		p.evalJSON(`JSON.stringify([...document.querySelectorAll('#table-state tbody tr')]
			.map((row) => [...row.cells].map((cell) => cell.textContent).join(' / ')).join(' | '))`, &rows)

		t.Errorf("the shipped closed state offers %q, which the server refuses; the rows are %s", closedActions, rows)
	}

	p.run("name a state", chromedp.SetValue(`#form-state [name="name"]`, "waiting for the customer", chromedp.ByQuery),
		p.chooseOption(`#form-state [name="type"]`, "pending-reminder"),
		p.click("#state-submit"))
	p.waitForText("#table-state tbody", "waiting for the customer")

	press := func(what, button string) {
		var pressed bool

		p.evalJSON(`JSON.stringify((() => {
			const row = [...document.querySelectorAll('#table-state tbody tr')]
				.find((row) => [...row.cells].some((cell) => cell.textContent === 'waiting for the customer'));
			const button = row?.querySelector('`+button+`');
			button?.click();
			return Boolean(button);
		})())`, &pressed)

		if !pressed {
			t.Fatalf("the new state's row has no button to %s", what)
		}
	}

	press("edit", "button.link:not(.danger)")
	p.run("the form holds it", chromedp.WaitVisible("#state-cancel", chromedp.ByID))

	if got := p.text("#state-form-title"); !strings.Contains(got, "waiting for the customer") {
		t.Errorf("the form's heading reads %q while the state is open", got)
	}

	p.run("retire it", chromedp.Click(`#form-state [name="valid"]`, chromedp.ByQuery), p.click("#state-submit"))

	retired := false

	for deadline := time.Now().Add(waitPatience); time.Now().Before(deadline) && !retired; {
		var valid string

		p.evalJSON(`JSON.stringify([...document.querySelectorAll('#table-state tbody tr')]
			.filter((row) => [...row.cells].some((cell) => cell.textContent === 'waiting for the customer'))
			.map((row) => row.cells[row.cells.length - 2].textContent).join(''))`, &valid)

		retired = valid == "no"
		if !retired {
			time.Sleep(200 * time.Millisecond)
		}
	}

	if !retired {
		t.Fatal("unticking the switch did not make the state invalid")
	}

	if p.visible("#state-cancel") {
		t.Error("the form still holds the state after it was saved")
	}

	press("delete", "button.danger")
	p.run("confirm", chromedp.WaitVisible(".confirm-overlay", chromedp.ByQuery),
		p.click(".confirm-actions button.danger"))
	p.waitGone(".confirm-overlay")

	for deadline := time.Now().Add(waitPatience); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if !strings.Contains(p.text("#table-state tbody"), "waiting for the customer") {
			return
		}
	}

	t.Error("confirming the delete left the state in the table")
}

// What ships is named in the reader's language: the states, the priorities, the
// types and the types a state can have are identifiers in the database, and a
// German reader is shown German words for them.
func TestTheTicketSetupNamesWhatShipsInTheReadersLanguage(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()
	p.chooseLanguage("de")

	p.run("open the ticket setup", p.click(`.tab[data-view="ticketsetup"]`),
		chromedp.WaitVisible("#table-state", chromedp.ByID))
	p.waitForText("#table-state tbody", "warten auf Erinnerung")

	for table, want := range map[string]string{
		"#table-state tbody":    "wartend, mit Erinnerung",
		"#table-priority tbody": "sehr hoch",
		"#table-type tbody":     "Serviceanfrage",
	} {
		if got := p.text(table); !strings.Contains(got, want) {
			t.Errorf("%s does not say %q: %q", table, want, got)
		}
	}

	if got := p.text("#table-priority tbody"); strings.Contains(got, "very-high") {
		t.Errorf("a priority is shown by its identifier: %q", got)
	}
}

// The queue card makes a queue under another with access for one role, offered
// to customers in the portal, shows it by its place in the tree and who reaches
// it, keeps the portal ticked when it is corrected, moves it to the top - which
// the form has to say out loud - and deletes it.
func TestTheQueueCardPlacesAQueueAndSaysWhoReachesIt(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the ticket setup", p.click(`.tab[data-view="ticketsetup"]`),
		chromedp.WaitVisible("#table-queue", chromedp.ByID))
	p.waitForText("#table-queue tbody", "Service Desk")

	var agentRole string

	p.evalJSON(`JSON.stringify(document.querySelector('#queue-access-list select[data-role]') ?
		[...document.querySelectorAll('#queue-access-list label')]
			.filter((label) => label.textContent.startsWith('Agent'))
			.map((label) => label.querySelector('select').dataset.role).join('') : '')`, &agentRole)

	if agentRole == "" {
		t.Fatal("the queue form offers no access for the agents")
	}

	p.run("describe a queue", chromedp.SetValue(`#form-queue [name="name"]`, "Second level", chromedp.ByQuery),
		p.chooseOption(`#form-queue [name="parentId"]`, p.queueID("Service Desk")),
		p.chooseOption(`#queue-access-list select[data-role="`+agentRole+`"]`, "read"),
		chromedp.Click(`#form-queue [name="portal"]`, chromedp.ByQuery),
		p.click("#queue-submit"))
	p.waitForText("#table-queue tbody", "Service Desk › Second level")

	if row := p.queueRow("Service Desk › Second level"); !strings.Contains(row, "reads its tickets") {
		t.Errorf("the new queue's row does not say the agents read it: %q", row)
	}

	p.pressInQueueRow("Service Desk › Second level", "button.link:not(.danger)")
	p.run("the form holds it", chromedp.WaitVisible("#queue-cancel", chromedp.ByID))

	var offered bool

	p.run("read whether customers open tickets in it",
		chromedp.Evaluate(`document.querySelector('#form-queue [name="portal"]').checked`, &offered))

	if !offered {
		t.Error("the queue opened for correcting is no longer offered in the portal")
	}

	p.run("move it to the top", p.chooseOption(`#form-queue [name="parentId"]`, ""), p.click("#queue-submit"))

	moved := false

	for deadline := time.Now().Add(waitPatience); time.Now().Before(deadline) && !moved; {
		moved = p.queueRow("Second level") != "" && p.queueRow("Service Desk › Second level") == ""
		if !moved {
			time.Sleep(200 * time.Millisecond)
		}
	}

	if !moved {
		t.Fatal("choosing the top did not move the queue out from under the desk")
	}

	p.pressInQueueRow("Second level", "button.danger")
	p.run("confirm", chromedp.WaitVisible(".confirm-overlay", chromedp.ByQuery),
		p.click(".confirm-actions button.danger"))
	p.waitGone(".confirm-overlay")

	for deadline := time.Now().Add(waitPatience); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if p.queueRow("Second level") == "" {
			return
		}
	}

	t.Error("confirming the delete left the queue in the table")
}

// queueID is the id the queue card's parent choices give the queue with this
// path, as the value to choose.
func (p *page) queueID(path string) string {
	var id string

	p.evalJSON(`JSON.stringify([...document.querySelectorAll('#form-queue [name="parentId"] option')]
		.filter((option) => option.textContent === `+jsString(path)+`)
		.map((option) => option.value).join(''))`, &id)

	if id == "" {
		p.t.Fatalf("the queue form offers nothing called %q to sit under", path)
	}

	return id
}

// queueRow is the text of the queue card's row for the queue with this path, or
// nothing where there is none.
func (p *page) queueRow(path string) string {
	var row string

	p.evalJSON(`JSON.stringify([...document.querySelectorAll('#table-queue tbody tr')]
		.filter((row) => [...row.cells].some((cell) => cell.textContent === `+jsString(path)+`))
		.map((row) => row.textContent).join(''))`, &row)

	return row
}

// pressInQueueRow presses a button in the queue card's row for this path.
func (p *page) pressInQueueRow(path, button string) {
	var pressed bool

	p.evalJSON(`JSON.stringify((() => {
		const row = [...document.querySelectorAll('#table-queue tbody tr')]
			.find((row) => [...row.cells].some((cell) => cell.textContent === `+jsString(path)+`));
		const button = row?.querySelector('`+button+`');
		button?.click();
		return Boolean(button);
	})())`, &pressed)

	if !pressed {
		p.t.Fatalf("the row of %q has no %s", path, button)
	}
}

// jsString writes a Go string as a JavaScript string literal, quotes and all.
func jsString(s string) string {
	quoted, _ := json.Marshal(s)

	return string(quoted)
}

// The ticket setup screen names the state types a lock runs out in: new and
// open are on and cannot be switched off, another switched on is saved and
// stays on once the screen is drawn again.
func TestTheTicketSetupNamesWhereALockRunsOut(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the ticket setup", p.click(`.tab[data-view="ticketsetup"]`),
		chromedp.WaitVisible("#unlock-card", chromedp.ByID))

	switches := func() string {
		var state string

		p.evalJSON(`JSON.stringify([...document.querySelectorAll('#unlock-types input[name="types"]')]
			.map((box) => box.value + (box.checked ? '+' : '-') + (box.disabled ? '!' : '')).join(' '))`, &state)

		return state
	}

	deadline := time.Now().Add(waitPatience)
	for !strings.Contains(switches(), "new+!") && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}

	if got := switches(); got != "new+! open+! pending-reminder- pending-auto- closed- removed- merged-" {
		t.Fatalf("the switches read %q; want new and open on and fixed, the rest off", got)
	}

	p.run("switch on the waiting type and save", chromedp.Click(`#unlock-types input[value="pending-reminder"]`, chromedp.ByQuery),
		p.click(`#form-unlock-types button[type="submit"]`))

	var kept []struct {
		Type     string `json:"type"`
		IsSystem bool   `json:"isSystem"`
	}

	deadline = time.Now().Add(waitPatience)
	for len(kept) != 3 && time.Now().Before(deadline) {
		p.awaitJSON(`api('/unlock-state-types').then((list) => list.items)`, &kept)
		time.Sleep(250 * time.Millisecond)
	}

	if len(kept) != 3 || kept[2].Type != "pending-reminder" || kept[2].IsSystem {
		t.Fatalf("the server keeps %+v; want the waiting type beside new and open", kept)
	}

	p.run("draw the screen again", chromedp.Evaluate(`refreshAll()`, nil))

	deadline = time.Now().Add(waitPatience)
	for !strings.Contains(switches(), "pending-reminder+") && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}

	if got := switches(); !strings.Contains(got, "pending-reminder+") {
		t.Errorf("drawn again the switches read %q; want the waiting type on", got)
	}
}

// A waiting state is made with the state it moves a ticket on to: the choice
// shows only for that type, is needed there, offers only states that can end a
// wait, and the table says where the state moves a ticket on to.
func TestAWaitingStateIsMadeWithWhereItMovesOn(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the ticket setup", p.click(`.tab[data-view="ticketsetup"]`),
		chromedp.WaitVisible("#table-state", chromedp.ByID))
	p.waitForText("#table-state tbody", "pending reminder")

	if p.visible("#state-next") {
		t.Error("the choice of where a state moves a ticket on to shows for a new state")
	}

	p.run("name a waiting state", chromedp.SetValue(`#form-state [name="name"]`, "waiting to reopen", chromedp.ByQuery),
		p.chooseOption(`#form-state [name="type"]`, "pending-auto"))

	if !p.visible("#state-next") {
		t.Fatal("the choice of where a waiting state moves a ticket on to did not show")
	}

	var offered []string

	p.evalJSON(`JSON.stringify([...document.querySelectorAll('#form-state [name="nextStateId"] option')]
		.map((option) => option.textContent))`, &offered)

	for _, waits := range []string{"pending reminder", "merged"} {
		if slices.Contains(offered, waits) {
			t.Errorf("the next states offered are %q, %s among them", offered, waits)
		}
	}

	var openID string

	p.evalJSON(`JSON.stringify(String(cache.ticketStates.find((state) => state.name === 'open').id))`, &openID)

	p.run("send it on to open", p.chooseOption(`#form-state [name="nextStateId"]`, openID), p.click("#state-submit"))
	p.waitForText("#table-state tbody", "waiting to reopen")

	var row string

	p.evalJSON(`JSON.stringify([...document.querySelectorAll('#table-state tbody tr')]
		.find((row) => row.textContent.includes('waiting to reopen')).textContent)`, &row)

	if !strings.Contains(row, "→ open") {
		t.Errorf("the waiting state's row reads %q; want it saying it moves on to open", row)
	}
}
