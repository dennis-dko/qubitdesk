//go:build browser

package browser

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// A ticket is linked to the item it concerns from the ticket's screen, the link
// reads the other way round on the item's, which the link's name opens, and it
// is undone there.
func TestATicketIsLinkedToAnItemOnItsScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var seeded bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const queue = (await api('/queues')).items[0];
		await send('/tickets', { title: 'Printer', queueId: queue.id, body: 'It jams.' });
		const place = (await api('/cmdb/classes')).items.find((entry) => entry.name === 'location');
		const definition = (await api('/cmdb/classes/' + place.id + '/definitions')).items[0];
		const least = (attributes) => Object.fromEntries(attributes.filter((a) => a.countMin > 0).map((a) => [a.key,
			Array.from({ length: a.countMin }, () => (a.sub && a.sub.length ? { sub: least(a.sub) } : {}))]));
		const offered = async (list) => (await api('/general-catalog?class=' + list)).items.find((v) => v.valid).id;
		await send('/cmdb/items', { classId: place.id, name: 'Head office',
			deploymentStateId: await offered('ITSM::ConfigItem::DeploymentState'),
			incidentStateId: await offered('ITSM::Core::IncidentState'), data: least(definition.attributes) });
		await refreshAll();
		return true;
	})()`, &seeded)

	p.run("open the ticket", p.click(`.tab[data-view="tickets"]`), p.click(`#table-tickets tbody button.link`),
		chromedp.WaitVisible("#form-ticket-link", chromedp.ByID))
	p.waitForText("#ticket-links .link-list", "Nothing linked yet.")

	p.run("find the office", chromedp.Evaluate(`(() => {
		const form = document.querySelector('#form-ticket-link');
		form.elements.type.value = 'DependsOn';
		form.elements.find.value = 'office';
		form.elements.find.dispatchEvent(new Event('input', { bubbles: true }));
	})()`, nil))
	p.waitEvaluates("the office offered", `String(document.querySelector('#form-ticket-link [name="record"]').options.length)`,
		"2")
	p.run("link it", chromedp.Evaluate(`(() => {
		const form = document.querySelector('#form-ticket-link');
		form.elements.record.selectedIndex = 1;
	})()`, nil), p.click(`#form-ticket-link button[type="submit"]`))
	p.waitForText("#ticket-links .link-list", "depends on 00000001 Head office")

	p.run("open the office from the link", p.click(`#ticket-links .link-list li button.link`),
		chromedp.WaitVisible("#configitem-detail", chromedp.ByID))
	p.waitForText("#configitem-links .link-list", "required for")
	p.waitForText("#configitem-links .link-list", "Printer")

	p.run("undo it from the office", p.click(`#configitem-links .link-list li button.link:last-child`))
	p.waitForText("#configitem-links .link-list", "Nothing linked yet.")
}
