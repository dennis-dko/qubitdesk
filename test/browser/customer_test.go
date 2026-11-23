//go:build browser

package browser

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// An organisation and a contact are made on the customer screen, a ticket is
// opened for the contact - found by typing - and names them both, and deleting
// the contact asks a second time, with how many tickets, before the ticket is
// left for the organisation alone.
func TestACustomerIsMadeGivenATicketAndDeletedWithItsTicketKept(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the customers", p.click(`.tab[data-view="customers"]`),
		chromedp.WaitVisible("#form-organisation", chromedp.ByID))

	p.run("make an organisation",
		chromedp.SetValue(`#form-organisation [name="name"]`, "Acme Industries", chromedp.ByQuery),
		chromedp.SetValue(`#form-organisation [name="url"]`, "https://acme.example", chromedp.ByQuery),
		p.click("#organisation-submit"))
	p.waitForText("#table-organisation tbody", "Acme Industries")

	if link := p.text(`#table-organisation tbody a[href="https://acme.example"]`); link == "" {
		t.Error("the organisation's website is not shown as a link to it")
	}

	p.run("make a contact at it",
		chromedp.SetValue(`#form-contact [name="name"]`, "Ann Anders", chromedp.ByQuery),
		chromedp.SetValue(`#form-contact [name="email"]`, "ann@acme.example", chromedp.ByQuery),
		p.chooseOptionByText(`#form-contact [name="organisationId"]`, "Acme Industries"),
		p.click("#contact-submit"))
	p.waitForText("#table-contact tbody", "Ann Anders")

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#form-ticket", chromedp.ByID))
	p.waitForValue(`#form-ticket [name="queueId"]`)

	p.run("find Ann", chromedp.SendKeys(`#form-ticket [name="contactSearch"]`, "ann", chromedp.ByQuery))
	p.run("choose her", p.chooseOptionByText(`#form-ticket [name="contactId"]`, "Ann Anders <ann@acme.example>"))

	p.run("open a ticket for her",
		chromedp.SetValue(`#form-ticket [name="title"]`, "Badge reader broken", chromedp.ByQuery),
		chromedp.SetValue(`#form-ticket [name="body"]`, "The badge reader at the door is dead.", chromedp.ByQuery),
		p.click("#ticket-submit"))
	p.run("it opens", chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForText("#ticket-facts", "Ann Anders")
	p.waitForText("#ticket-facts", "Acme Industries")

	p.run("back to the customers", p.click(`.tab[data-view="customers"]`),
		chromedp.WaitVisible("#table-contact", chromedp.ByID))
	p.run("delete Ann", chromedp.Evaluate(`[...document.querySelectorAll('#table-contact tbody tr')]
		.find((row) => row.textContent.includes('Ann Anders')).querySelector('button.danger').click()`, nil))
	p.run("confirm once", chromedp.WaitVisible(".confirm-overlay", chromedp.ByQuery),
		p.click(".confirm-actions button.danger"))

	// The second question is the server's, with the count in it.
	p.run("asked again", chromedp.WaitVisible(".confirm-overlay", chromedp.ByQuery))

	if question := p.text(".confirm-card"); !strings.Contains(question, "1") {
		t.Errorf("the second question does not say how many tickets are for Ann: %q", question)
	}

	p.run("confirm again", p.click(".confirm-actions button.danger"))
	p.waitGone(".confirm-overlay")

	for deadline := time.Now().Add(waitPatience); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if !strings.Contains(p.text("#table-contact tbody"), "Ann Anders") {
			return
		}
	}

	t.Error("confirming twice left Ann in the table")
}

// Saving a contact or a ticket keeps an organisation that no list holds.
//
// A listing answers two hundred at most, and the screen first filled its
// choices of organisation from one: a contact of the organisation past the two
// hundredth was drawn as working nowhere, and saving her - or a ticket for her -
// took the organisation off. Built as it happened, with two hundred
// organisations sorted before the one that matters.
func TestSavingKeepsAnOrganisationNoListHolds(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made struct {
		Organisation uint `json:"organisation"`
		Contact      uint `json:"contact"`
		Ticket       uint `json:"ticket"`
	}

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		for (let i = 1; i <= 200; i++) await send('/organisations', { name: 'Aardvark ' + String(i).padStart(3, '0') });
		const zeta = await send('/organisations', { name: 'Zeta Works' });
		const zoe = await send('/contacts', { name: 'Zoe Zimmer', organisationId: zeta.id });
		const queue = (await api('/queues')).items[0];
		const ticket = await send('/tickets',
			{ title: 'Lift stuck', queueId: queue.id, body: 'Between two floors.', contactId: zoe.id });
		await refreshAll();
		return { organisation: zeta.id, contact: zoe.id, ticket: ticket.id };
	})()`, &made)

	p.run("open the customers", p.click(`.tab[data-view="customers"]`),
		chromedp.WaitVisible("#table-contact", chromedp.ByID))
	p.waitForText("#table-contact tbody", "Zoe Zimmer")

	if row := p.text("#table-contact tbody"); !strings.Contains(row, "Zeta Works") {
		t.Errorf("Zoe's row does not name her organisation: %q", row)
	}

	p.run("correct Zoe", chromedp.Evaluate(`[...document.querySelectorAll('#table-contact tbody tr')]
		.find((row) => row.textContent.includes('Zoe Zimmer')).querySelector('button.link:not(.danger)').click()`, nil),
		chromedp.SetValue(`#form-contact [name="phone"]`, "+49 30 9", chromedp.ByQuery),
		p.click("#contact-submit"))
	p.waitForText("#table-contact tbody", "+49 30 9")

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitForText("#table-tickets tbody", "Lift stuck")
	p.run("open the lift", chromedp.Evaluate(`[...document.querySelectorAll('#table-tickets tbody tr')]
		.find((row) => row.textContent.includes('Lift stuck')).querySelector('button.link').click()`, nil),
		chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForValue(`#form-ticket [name="id"]`)

	p.run("correct its title",
		chromedp.SetValue(`#form-ticket [name="title"]`, "Lift stuck again", chromedp.ByQuery),
		p.click("#ticket-submit"))
	// Waited for on the server, which is what the rest of the case reads. It
	// waited for the list, which is drawn again only after the ticket has been
	// read back, and once in a full-suite run the list went on showing the old
	// title for the whole wait - for a cause that neither a late read of the list
	// nor a scroll still running at the click reproduced. Should the save itself
	// ever not land, the form, the screen and the server are reported with it.
	for deadline := time.Now().Add(waitPatience); ; time.Sleep(300 * time.Millisecond) {
		var now struct {
			Server string   `json:"server"`
			ID     string   `json:"id"`
			Title  string   `json:"title"`
			Toasts []string `json:"toasts"`
		}

		p.awaitJSON(fmt.Sprintf(`(async () => {
			const form = document.querySelector('#form-ticket');
			return {
				server: (await api('/tickets/%d')).title, id: form.elements.id.value, title: form.elements.title.value,
				toasts: [...document.querySelectorAll('.toast-note')].map((note) => note.textContent.trim()),
			};
		})()`, made.Ticket), &now)

		if now.Server == "Lift stuck again" {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("the corrected title never reached the server: %+v", now)
		}
	}

	var saved struct {
		Contact struct {
			OrganisationID *uint `json:"organisationId"`
		} `json:"contact"`
		Ticket struct {
			OrganisationID *uint `json:"organisationId"`
		} `json:"ticket"`
	}

	p.awaitJSON(fmt.Sprintf(`(async () => ({ contact: await api('/contacts/%d'), ticket: await api('/tickets/%d') }))()`,
		made.Contact, made.Ticket), &saved)

	for what, got := range map[string]*uint{"Zoe": saved.Contact.OrganisationID, "her ticket": saved.Ticket.OrganisationID} {
		if got == nil || *got != made.Organisation {
			t.Errorf("saving %s left the organisation %v, want %d", what, got, made.Organisation)
		}
	}
}

// chooseOptionByText chooses the option of a select that reads text, once the
// select offers it: the choices of a search arrive a moment after the typing.
func (p *page) chooseOptionByText(selector, text string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		deadline := time.Now().Add(waitPatience)

		for {
			var value string

			if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(
				`[...(document.querySelector(%s)?.options ?? [])].find((o) => o.textContent === %s)?.value ?? ''`,
				jsString(selector), jsString(text)), &value)); err != nil {
				return err
			}

			if value != "" {
				return chromedp.Run(ctx, chromedp.SetValue(selector, value, chromedp.ByQuery))
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("%s never offered %q", selector, text)
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	})
}
