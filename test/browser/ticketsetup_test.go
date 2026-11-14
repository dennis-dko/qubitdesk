//go:build browser

package browser

import (
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
