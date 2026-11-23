//go:build browser

package browser

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dennis-dko/qubitdesk/internal/support/spreadsheet"
)

// An import checked by one account leaves nothing behind for the next.
//
// The import cards are not forms, so the sign-out's form reset never reached
// them: the file stayed chosen, the verdict stayed written and the Import button
// stayed offered - a clean verdict brings it with it. The checked rows went too
// little further: the sign-out redraws for the language, which drew a kept
// verdict straight back, and the tables' own cards were never emptied at all. So
// whoever signed in next found the last person's rows and a button that would
// write them as their own.
func TestASignOutTakesAnImportsFileAndVerdictWithIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	people, err := spreadsheet.WriteUsers("en", []spreadsheet.UserRow{
		{Name: "A name of somebody else's", Email: "else@example.com", Role: "agent"},
	})
	if err != nil {
		t.Fatalf("building the people: %v", err)
	}

	roles, err := spreadsheet.WriteRoles("en", []string{"tickets:read"}, []spreadsheet.RoleRow{
		{Name: "elsewhere", Description: "A role of somebody else's", Granted: []string{"tickets:read"}},
	})
	if err != nil {
		t.Fatalf("building the roles: %v", err)
	}

	peoplePath := filepath.Join(dir, "people.xlsx")
	rolesPath := filepath.Join(dir, "roles.xlsx")

	for path, book := range map[string][]byte{peoplePath: people, rolesPath: roles} {
		if err := os.WriteFile(path, book, 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	p := open(t)
	p.readyAdmin()

	const (
		peopleCard = `#view-users .card:has(input[type="file"])`
		rolesCard  = `#view-roles .card:has(input[type="file"])`
	)

	p.run("check a file of people", p.click(`.tab[data-view="users"]`),
		chromedp.WaitVisible(peopleCard, chromedp.ByQuery),
		chromedp.SetUploadFiles(peopleCard+` input[type="file"]`, []string{peoplePath}, chromedp.ByQuery),
		p.click(peopleCard+` button[data-i18n="wb.preview"]`))
	p.waitForText(peopleCard+" tbody", "A name of somebody else's")

	p.run("check a file of roles", p.click(`.tab[data-view="roles"]`),
		chromedp.WaitVisible(rolesCard, chromedp.ByQuery),
		chromedp.SetUploadFiles(rolesCard+` input[type="file"]`, []string{rolesPath}, chromedp.ByQuery),
		p.click(rolesCard+` button[data-i18n="wb.preview"]`))
	p.waitForText(rolesCard+" tbody", "A role of somebody else's")

	p.run("sign out", p.click("#logout"), chromedp.WaitVisible("#form-login", chromedp.ByID))

	var left struct {
		Rows, Files int
		Offered     bool
		Said        string
	}

	p.run("read what the next account would find", chromedp.Evaluate(`(() => {
		const cards = ['`+peopleCard+`', '`+rolesCard+`'].map((s) => document.querySelector(s));

		return {
			rows: cards.filter((c) => /somebody else's/.test(c.querySelector('tbody').textContent)).length,
			files: cards.reduce((n, c) => n + c.querySelector('input[type="file"]').files.length, 0),
			offered: cards.some((c) => !c.querySelector('button[data-i18n="wb.import"]').hidden),
			said: cards.map((c) => c.querySelector('p.muted:not([data-i18n])')?.textContent ?? '').join(''),
		};
	})()`, &left))

	if left.Rows > 0 {
		t.Errorf("%d import card(s) still show the rows the last account checked", left.Rows)
	}

	if left.Files > 0 {
		t.Errorf("%d file(s) are still chosen for whoever signs in next", left.Files)
	}

	if left.Offered {
		t.Error("an Import button is still offered for the last account's file")
	}

	if left.Said != "" {
		t.Errorf("the verdict on the last account's file is still written: %q", left.Said)
	}
}

// Every form left correcting a record goes back to creating one when its
// account signs out.
//
// The sign-out resets every form, and a reset leaves a hidden field's value
// where it was - which is why each form's own reset clears its id by hand. Those
// resets were not called on the way out, so a form somebody had been correcting
// kept the record's id under its "edit" heading. Whoever signed in next and
// filled it in to make something new changed the record the last person had
// open: an administrator adding an account overwrote the one their predecessor
// was correcting.
//
// It held the account and the role form alone while the ticket system added
// seven more of the shape - the ticket, the queue, the three catalogs, the
// organisation and the contact - and the sign-out reset none of them; the change
// and its work order were the next two. So the
// forms are found by the hidden id they hold rather than named, a record is
// opened in each, and a form the case cannot open one in stops it: the next form
// of the kind is taught to the case instead of passed over by it.
func TestASignOutPutsEveryFormBackToCreating(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		await send('/users', { name: 'Wilma', email: 'wilma@example.com', role: 'agent', password: 'wilma-password-1' });
		const acme = await send('/organisations', { name: 'Acme' });
		const ann = await send('/contacts', { name: 'Ann', organisationId: acme.id });
		const queue = (await api('/queues')).items[0];
		await send('/tickets', { title: 'Printer', queueId: queue.id, body: 'It jams.', contactId: ann.id });
		await send('/calendars', { name: 'Office', hours: [{ weekday: 1, from: '08:00', to: '17:00' }] });
		await send('/service-levels', { name: 'Gold', solutionMinutes: 480 });
		const escalate = await send('/macros', { name: 'Escalate', actions: [] });
		await send('/jobs', { name: 'Escalate the desk', criteria: { queueIds: [queue.id] }, macroIds: [escalate.id] });
		await send('/response-templates', { name: 'Thanks', body: 'Thank you.' });
		const printers = await send('/faq/categories', { name: 'Printers' });
		await send('/faq/articles', { categoryId: printers.id, title: 'Toner', body: 'Shake it.' });
		await send('/notification-definitions', { name: 'Opened', events: ['TicketCreate'],
			recipients: ['AgentOwner'], messages: [{ language: 'en', subject: 'New', body: 'Opened.' }] });
		await send('/exec-plans', { name: 'Mornings', type: 'TimeBased', weekdays: [1], times: ['08:00'] });
		const place = (await api('/cmdb/classes')).items.find((entry) => entry.name === 'location');
		const definition = (await api('/cmdb/classes/' + place.id + '/definitions')).items[0];
		const least = (attributes) => Object.fromEntries(attributes.filter((a) => a.countMin > 0).map((a) => [a.key,
			Array.from({ length: a.countMin }, () => ({
				...(a.required ? { value: a.type === 'Date' ? '2026-11-21' : 'kept' } : {}),
				...(a.sub && a.sub.length ? { sub: least(a.sub) } : {}),
			}))]));
		const offered = async (list) => (await api('/general-catalog?class=' + list)).items.find((v) => v.valid).id;
		await send('/cmdb/items', { classId: place.id, name: 'Head office',
			deploymentStateId: await offered('ITSM::ConfigItem::DeploymentState'),
			incidentStateId: await offered('ITSM::Core::IncidentState'), data: least(definition.attributes) });
		const swap = await send('/changes', { title: 'Swap the switch' });
		await send('/changes/' + swap.id + '/work-orders', { title: 'Install it' });
		await refreshAll();
		return true;
	})()`, &made)

	// Each by what a person would press: a row's edit button - the
	// administrator's own account offers none, so Wilma's, and a shipped state
	// offers none, so the first that does - and for the ticket form the
	// ticket's number in the list, because opening a ticket puts it there.
	openers := map[string]string{
		"form-user":         `#table-users tbody button[data-action="edit"]`,
		"form-role":         `#table-roles tbody td.actions button`,
		"form-queue":        `#table-queue tbody td.actions button.link:not(.danger)`,
		"form-state":        `#table-state tbody td.actions button.link:not(.danger)`,
		"form-priority":     `#table-priority tbody td.actions button.link:not(.danger)`,
		"form-type":         `#table-type tbody td.actions button.link:not(.danger)`,
		"form-organisation": `#table-organisation tbody td.actions button.link:not(.danger)`,
		"form-contact":      `#table-contact tbody td.actions button.link:not(.danger)`,
		"form-ticket":       `#table-tickets tbody button.link`,
		"form-sysconfig":    `#table-sysconfig tbody td.actions button.link`,
		"form-calendar":     `#table-calendar tbody td.actions button.link:not(.danger)`,
		"form-sla":          `#table-sla tbody td.actions button.link:not(.danger)`,
		"form-macro":        `#table-macro tbody td.actions button.link:not(.danger)`,
		"form-plan":         `#table-plan tbody td.actions button.link:not(.danger)`,
		"form-job":          `#table-job tbody td.actions button.link:not(.danger)`,
		"form-notification": `#table-notification tbody td.actions button.link:not(.danger)`,
		"form-template":     `#table-template tbody td.actions button.link:not(.danger)`,
		"form-faq-category": `#table-faq-category tbody td.actions button.link:not(.danger)`,
		"form-faq-article":  `#table-faq tbody td.actions button.link:not(.danger)`,
		"form-catalog":      `#table-catalog tbody td.actions button.link`,
		"form-configitem":   `#table-configitem tbody button.link`,
	}

	for form, opener := range openers {
		p.run("open a record in "+form, chromedp.Evaluate(
			fmt.Sprintf(`void document.querySelector(%s)?.click()`, jsString(opener)), nil))
	}

	// A work order's row is drawn only once its change has been read, so one
	// script opens the change, waits for it and opens the work order, rather
	// than a click on a row that is not there yet.
	scripted := []string{"form-itsmchange", "form-workorder"}

	var opened bool

	p.awaitJSON(`(async () => {
		await openItsmChange((await api('/changes')).items[0].id);
		document.querySelector('#table-workorder tbody button.link').click();
		return true;
	})()`, &opened)

	const holdingAnID = `[...document.querySelectorAll('form')]
		.filter((form) => form.querySelector('input[type="hidden"][name="id"]'))`

	// The ticket arrives in its form once it has been read, so this waits.
	var correcting map[string]string

	for deadline := time.Now().Add(waitPatience); ; time.Sleep(200 * time.Millisecond) {
		p.evalJSON(`JSON.stringify(Object.fromEntries(`+holdingAnID+`
			.map((form) => [form.getAttribute('id'), form.elements.id.value])))`, &correcting)

		if !slices.Contains(slices.Collect(maps.Values(correcting)), "") || time.Now().After(deadline) {
			break
		}
	}

	for form, id := range correcting {
		if _, known := openers[form]; !known && !slices.Contains(scripted, form) {
			t.Errorf("%s holds a hidden id and this case does not know how to open a record in it - teach it", form)
		} else if id == "" {
			t.Errorf("this case starts from every form correcting a record, and %s is not", form)
		}
	}

	for _, form := range slices.Concat(slices.Collect(maps.Keys(openers)), scripted) {
		if _, there := correcting[form]; !there {
			t.Errorf("this case opens a record in %s, which holds no hidden id any more", form)
		}
	}

	if t.Failed() {
		t.FailNow()
	}

	p.run("sign out", p.click("#logout"), chromedp.WaitVisible("#form-login", chromedp.ByID))

	// What the next account would find: an id, a way to cancel the editing, and
	// a heading drawn from the record rather than read from its key.
	var left []string

	p.evalJSON(`JSON.stringify(`+holdingAnID+`.flatMap((form) => {
		const name = form.getAttribute('id');
		const left = [];
		const cancel = form.querySelector('[data-i18n="action.cancelEdit"]');
		const heading = document.querySelector('#' + name.replace(/^form-/, '') + '-form-title');

		if (form.elements.id.value) left.push(name + ' still holds the id ' + form.elements.id.value);
		if (cancel && !cancel.hidden) left.push(name + ' still offers to cancel the editing');
		if (heading && !heading.dataset.i18n) left.push(name + ' is still headed ' + JSON.stringify(heading.textContent.trim()));

		return left;
	}))`, &left)

	for _, what := range left {
		t.Errorf("after the sign-out %s", what)
	}
}

// The ticket an account had open goes with it.
//
// Opening a ticket draws it into a card of its own and remembers which ticket it
// is, so that reading the list again can draw it again. The card is not a table
// and the memory is not a form, and the sign-out cleared neither: the ticket's
// heading, its facts, every article and its history stayed in the document under
// the sign-in screen, and whoever signed in next had the ticket opened for them -
// on the last person's screen rather than their own.
func TestASignOutTakesTheOpenTicketWithIt(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()
	p.createOrdinaryAccount(t, workerEmail, workerPassword)

	var made bool

	p.awaitJSON(`(async () => {
		const queue = (await api('/queues')).items[0];
		await api('/tickets', { method: 'POST', body: JSON.stringify(
			{ title: 'Printer', queueId: queue.id, body: 'It jams on page two.' }) });
		await refreshAll();
		return true;
	})()`, &made)

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitForText("#table-tickets tbody", "Printer")
	p.run("open the printer", chromedp.Evaluate(`[...document.querySelectorAll('#table-tickets tbody tr')]
		.find((row) => row.textContent.includes('Printer')).querySelector('button.link').click()`, nil),
		chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForText("#ticket-articles", "It jams on page two.")

	p.run("sign out", p.click("#logout"), chromedp.WaitVisible("#form-login", chromedp.ByID))

	if left := p.text("#ticket-detail"); strings.Contains(left, "Printer") || strings.Contains(left, "It jams") {
		t.Errorf("the open ticket is still in the document under the sign-in screen: %q", left)
	}

	// Agents work in the shipped queue, so a ticket remembered across the
	// sign-out is opened for this one rather than refused.
	p.signIn(workerEmail, workerPassword)
	p.waitGone("#login-screen")
	p.settled()
	p.settleWelcome()

	var shown bool

	p.run("is a ticket open", chromedp.Evaluate(`!document.querySelector('#ticket-detail').hidden`, &shown))

	if shown {
		t.Errorf("the last account's open ticket was opened for the next: %q", p.text("#ticket-heading"))
	}
}

// The page of the ticket list an account had reached goes with it as well.
//
// Every list that pages put its page back to the first on the way out but this
// one, so whoever signed in next at the same desk was shown the page the last
// person had turned to - the second of twenty-six tickets, a single row, under a
// count saying there were twenty-six.
func TestASignOutTakesTheTicketListBackToItsFirstPage(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		for (let i = 1; i <= 26; i++) {
			await api('/tickets', { method: 'POST', body: JSON.stringify(
				{ title: 'Ticket ' + i, queueId: desk.id, body: 'Number ' + i + '.' }) });
		}
		await refreshAll();
		return true;
	})()`, &made)

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitEvaluates("the first page", `String(document.querySelectorAll('#table-tickets tbody tr').length)`, "25")
	p.run("turn to the second page", p.click("#tickets-next"))
	p.waitEvaluates("the second page", `String(document.querySelectorAll('#table-tickets tbody tr').length)`, "1")

	p.run("sign out", p.click("#logout"), chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.readyAdminAgain()

	p.run("open the tickets again", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitEvaluates("the next account starts on the first page",
		`String(document.querySelectorAll('#table-tickets tbody tr').length)`, "25")
}

// The overview and the statistics an account was shown go with it as well:
// their counts and the names of the queues it reaches stay in the document under
// the sign-in screen otherwise, and an account that may not read tickets never
// draws them again, so the last person's would wait under a tab merely hidden.
func TestASignOutTakesTheOverviewWithIt(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		await api('/tickets', { method: 'POST', body: JSON.stringify(
			{ title: 'Printer', queueId: desk.id, body: 'It jams.', ownerId: me.user.id }) });
		await refreshAll();
		return true;
	})()`, &made)

	p.run("open the overview", p.click(`.tab[data-view="overview"]`),
		chromedp.WaitVisible("#overview-tiles", chromedp.ByID))
	p.waitForText("#table-overview-queues tbody", "Service Desk")
	p.showTabOf("#report-card")
	p.run("wait for the statistics", chromedp.WaitVisible("#report-tiles", chromedp.ByID))
	p.waitForText("#table-report-rows tbody", "Service Desk")

	p.run("sign out", p.click("#logout"), chromedp.WaitVisible("#form-login", chromedp.ByID))

	if left := p.text("#view-overview"); strings.Contains(left, "Service Desk") || p.count(".overview-tile") > 0 {
		t.Errorf("the overview is still in the document under the sign-in screen: %q", left)
	}

	if left := p.text("#overview-panel-statistics"); strings.Contains(left, "Service Desk") || p.count(".report-tile") > 0 {
		t.Errorf("the statistics are still in the document under the sign-in screen: %q", left)
	}
}

// The greeting an account was shown goes with it as well.
//
// The greeting names whoever it greeted, and is drawn again only when somebody
// arrives on it - so whoever signed in next at the same desk could open it on the
// last person's name. The sign-out's own comment counts what is left under a tab
// that is merely hidden as handed to the next person.
func TestASignOutTakesTheGreetingWithIt(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyWorker()

	p.run("be greeted", p.click("#app-title"))
	p.waitForFilled("#welcome-title")

	p.run("sign out", p.click("#logout"), chromedp.WaitVisible("#form-login", chromedp.ByID))

	if said := p.text("#welcome-title"); said != "" {
		t.Errorf("after the sign-out the greeting still says %q", said)
	}
}

// A sign-out takes the notices in the corner with it.
//
// A notice lingers for up to twenty seconds, and the stack sits above the sign-in
// screen on purpose - so whoever signed in next at the same desk read the last
// account's "Password changed", or a failure with that account's detail folded
// under it. Found by walking the interface: the administrator's password notice
// was still on screen after signing in as somebody else.
func TestASignOutTakesItsNoticesWithIt(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("raise a notice", chromedp.Evaluate(
		`toast('Something only this account was told')`, nil))
	p.waitShown("#toast .toast-note")

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID),
		chromedp.WaitVisible("#form-login", chromedp.ByID))

	if left := p.text("#toast"); left != "" {
		t.Errorf("the sign-in screen still carries the last session's notice: %q", left)
	}
}
