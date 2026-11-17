//go:build browser

package browser

import (
	"strconv"
	"testing"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

// A notification pushed late holds while the agent it was sent to has not read
// it: the service worker asks the installation about it before showing it, shows
// it while it is unread, and takes it back once it has been read - on this
// screen or another - because by then it is news no longer.
func TestALateNotificationIsShownWhileItIsUnread(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	// The agent is there before the ticket is opened, because a notification is
	// sent to whoever is around the ticket when it happens.
	p.createOrdinaryAccount(t, workerEmail, workerPassword)

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		await send('/notification-definitions', { name: 'Opened', events: ['TicketCreate'],
			recipients: ['AgentWritePermissions'],
			messages: [{ language: 'en', subject: 'New: <KIX_TICKET_Title>', body: 'Opened.' }] });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		await send('/tickets', { title: 'Printer', queueId: desk.id, body: 'It jams.' });
		return true;
	})()`, &made)

	if !made {
		t.Fatal("could not set the notification up and open the ticket")
	}

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID), chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.signIn(workerEmail, workerPassword)
	p.waitGone("#login-screen")
	p.settled()

	p.run("allow notifications from the installation",
		browser.SetPermission(&browser.PermissionDescriptor{Name: "notifications"}, browser.PermissionSettingGranted).
			WithOrigin(p.app.BaseURL()))
	p.waitEvaluates("the page's leave to notify", `Notification.permission`, "granted")

	var id uint

	p.awaitJSON(`(async () => (await api('/me/notifications')).items[0].id)()`, &id)

	about := strconv.FormatUint(uint64(id), 10)
	tag := "gts-notification-" + about
	late := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
	push := p.pusher()

	push.until("late, and not read yet", tag, "New: Printer", late, about)

	p.awaitJSON(`(async () => {
		await api('/me/notifications/`+about+`/read', { method: 'PUT' });
		return true;
	})()`, nil)

	push.once("late, and read since", tag, "New: Printer, again", late, about)
	p.waitForNoNotification(tag, "a late notification read since")
}
