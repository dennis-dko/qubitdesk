//go:build browser

package browser

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/serviceworker"
	"github.com/chromedp/chromedp"
)

// A notification pushed to a browser that was closed is held by the push service
// and handed over when the browser is started again, up to an hour later - by
// when what it says may have stopped being true. So one that arrives late is shown
// only once the installation has confirmed it, and one nothing here knows how to
// ask about takes back the notification shown for it before; one that arrives on
// time was confirmed the moment it was sent, and is shown as it is.
//
// What is not shown is seen as the earlier notification going away, so a push
// that never arrived fails here rather than passing for one the worker withheld.
func TestANotificationPushedLateIsShownOnlyWhileItCanBeConfirmed(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyWorker()

	p.run("allow notifications from the installation",
		browser.SetPermission(&browser.PermissionDescriptor{Name: "notifications"}, browser.PermissionSettingGranted).
			WithOrigin(p.app.BaseURL()))

	// The leave is the browser's to apply, and a push the worker could not show
	// for want of it would look exactly like one it chose not to show.
	p.waitEvaluates("the page's leave to notify", `Notification.permission`, "granted")

	push := p.pusher()
	onTime := time.Now().UTC().Format(time.RFC3339)
	late := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)

	push.until("on time", "qubitdesk-ticket-42", "on time", onTime, "42")
	push.once("late, about something nothing here can ask about", "qubitdesk-ticket-42", "late", late, "42")
	p.waitForNoNotification("qubitdesk-ticket-42", "a late notification nothing could confirm")
}

// pusher delivers pushes to the page's service worker, as the browser's own tools
// do.
type pusher struct {
	p            *page
	origin       string
	registration serviceworker.RegistrationID
}

func (p *page) pusher() pusher {
	p.t.Helper()

	return pusher{p: p, origin: strings.TrimSuffix(p.app.BaseURL(), "/"), registration: p.serviceWorkerRegistration()}
}

// once pushes a notification one time.
func (s pusher) once(what, tag, title, at, about string) {
	s.p.t.Helper()

	data, err := json.Marshal(map[string]string{"title": title, "body": what, "tag": tag, "at": at, "about": about})
	if err != nil {
		s.p.t.Fatal(err)
	}

	s.p.run("push "+what, serviceworker.DeliverPushMessage(s.origin, s.registration, string(data)))
}

// until pushes a notification that is shown without asking until it is shown. A
// browser now and then loses the first push its tools deliver to a new profile -
// measured: one run in three, while the second always arrived - and this is what
// makes sure the worker is reached before anything is concluded from its silence.
func (s pusher) until(what, tag, title, at, about string) {
	s.p.t.Helper()

	for deadline := time.Now().Add(waitPatience); time.Now().Before(deadline); {
		s.once(what, tag, title, at, about)

		for wait := time.Now().Add(3 * time.Second); time.Now().Before(wait); time.Sleep(200 * time.Millisecond) {
			for _, shown := range s.p.notifications(tag) {
				if shown == title {
					return
				}
			}
		}
	}

	s.p.t.Fatalf("pushed %q again and again, and the service worker never showed it", title)
}

// serviceWorkerRegistration is the registration of the page's service worker, as
// the browser's own tools name it to deliver a push to - once a version of it is
// activated, because a push delivered to one still installing is lost.
func (p *page) serviceWorkerRegistration() serviceworker.RegistrationID {
	p.t.Helper()

	// Bounded, and with what the browser says about the registration where it is
	// not ready: unbounded, a worker that never activated hung the case for the
	// whole of its time and said nothing about why. Where there is no
	// registration at all, the page's own attempt failed in silence - it means
	// to, an installation reached over plain HTTP simply goes without - so the
	// case registers again and says how that went, which is the browser's reason,
	// and does what the worker's install does from the page, which says why that
	// fails.
	var ready string

	p.awaitJSON(`Promise.race([
		navigator.serviceWorker.ready.then(() => 'ready'),
		new Promise((settle) => setTimeout(settle, 30000)).then(async () => {
			const registration = await navigator.serviceWorker.getRegistration();
			if (!registration) {
				const again = await navigator.serviceWorker.register('/sw.js').then(
					(made) => new Promise((settle) => setTimeout(settle, 5000)).then(() => 'registering again made '
						+ (made.active ? made.active.state : made.installing ? 'one still installing'
							: made.waiting ? 'one waiting' : 'nothing that lived')),
					(error) => 'registering again was refused: ' + error);

				// What the worker's install does - keep the shell - done from the page,
				// which may: the statuses of what it fetches, and why keeping them fails.
				const shell = ['/', '/app.js', '/app.css', '/theme.js', '/favicon.svg'];
				const statuses = await Promise.all(shell.map((path) => fetch(path).then(
					(res) => path + ' ' + res.status, (error) => path + ' ' + error)));
				const kept = await caches.open('probe').then((cache) => cache.addAll(shell)).then(
					() => 'kept', (error) => 'not kept: ' + error);

				return 'no registration for ' + location.href + '; ' + again + '; ' + statuses.join(', ') + '; '
					+ kept;
			}
			const state = (worker) => (worker ? worker.state : '-');
			return 'not ready after 30s: installing ' + state(registration.installing) + ', waiting '
				+ state(registration.waiting) + ', active ' + state(registration.active)
				+ ', controller ' + Boolean(navigator.serviceWorker.controller);
		}),
	])`, &ready)

	if ready != "ready" {
		p.t.Fatalf("the page's service worker is %s", ready)
	}

	scope := strings.TrimSuffix(p.app.BaseURL(), "/") + "/"

	// The listener runs on the browser's event loop and outlives this call, so it
	// only records and never waits.
	var (
		mu        sync.Mutex
		ours      serviceworker.RegistrationID
		activated = map[serviceworker.RegistrationID]bool{}
		changed   = make(chan struct{}, 1)
	)

	chromedp.ListenTarget(p.ctx, func(event any) {
		mu.Lock()

		switch updated := event.(type) {
		case *serviceworker.EventWorkerRegistrationUpdated:
			for _, registration := range updated.Registrations {
				if registration.ScopeURL == scope && !registration.IsDeleted {
					ours = registration.RegistrationID
				}
			}
		case *serviceworker.EventWorkerVersionUpdated:
			for _, version := range updated.Versions {
				activated[version.RegistrationID] = version.Status == serviceworker.VersionStatusActivated
			}
		}

		mu.Unlock()

		select {
		case changed <- struct{}{}:
		default:
		}
	})

	p.run("watch the service workers", serviceworker.Enable())

	deadline := time.After(waitPatience)

	for {
		mu.Lock()
		id, ready := ours, ours != "" && activated[ours]
		mu.Unlock()

		if ready {
			return id
		}

		select {
		case <-changed:
		case <-deadline:
			p.t.Fatalf("no activated service worker is registered for %s", scope)
		}
	}
}

// notifications are the titles of what the page's service worker shows under tag.
func (p *page) notifications(tag string) []string {
	p.t.Helper()

	var titles []string

	p.awaitJSON(`navigator.serviceWorker.ready
		.then((registration) => registration.getNotifications({ tag: '`+tag+`' }))
		.then((shown) => shown.map((notification) => notification.title))`, &titles)

	return titles
}

// waitForNoNotification waits until the service worker shows nothing under tag,
// and says what it shows instead where that does not happen.
func (p *page) waitForNoNotification(tag, what string) {
	p.t.Helper()

	var shown []string

	for deadline := time.Now().Add(waitPatience); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if shown = p.notifications(tag); len(shown) == 0 {
			return
		}
	}

	p.t.Errorf("%s is not taken back: the service worker shows %q", what, shown)
}
