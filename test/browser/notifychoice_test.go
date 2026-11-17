//go:build browser

package browser

import (
	"slices"
	"testing"

	"github.com/chromedp/chromedp"
)

// An agent chooses under My account what they are told: the queues they follow
// among those they reach, and which of the notifications that let them they
// take. What they chose is kept, and drawn again as they left it.
func TestAnAgentChoosesWhatTheyAreToldUnderMyAccount(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		await api('/notification-definitions', { method: 'POST', body: JSON.stringify({ name: 'Opened, if you like',
			events: ['TicketCreate'], recipients: ['AgentWritePermissions'], optional: true,
			messages: [{ language: 'en', subject: 'New', body: 'Opened.' }] }) });
		return true;
	})()`, &made)

	if !made {
		t.Fatal("could not set the notification up")
	}

	p.becomeWorker()

	p.run("open my account", p.click(`.tab[data-view="settings"]`),
		chromedp.WaitVisible("#form-notify-choices", chromedp.ByID))
	p.waitForText("#notify-optional", "Opened, if you like")

	var taken bool

	p.evalJSON(`JSON.stringify(document.querySelector('#notify-optional input').checked)`, &taken)

	if !taken {
		t.Error("a notification nobody switched off is shown switched off")
	}

	p.run("follow the desk and leave the notification",
		chromedp.Evaluate(`document.querySelector('#notify-optional input').click();
			[...document.querySelectorAll('#notify-queues label')]
				.find((label) => label.textContent.includes('Service Desk')).querySelector('input').click();`, nil),
		p.click(`#form-notify-choices button[type="submit"]`))
	p.waitForText("#toast", "Saved what you are told")

	var chosen struct {
		SwitchedOff []uint `json:"switchedOff"`
		Followed    []uint `json:"followed"`
		Optional    uint   `json:"optional"`
		Desk        uint   `json:"desk"`
	}

	p.awaitJSON(`(async () => {
		const choices = await api('/me/notification-choices');
		return { switchedOff: choices.switchedOff, followed: choices.followed, optional: choices.optional[0].id,
			desk: choices.queues.find((queue) => queue.name === 'Service Desk').id };
	})()`, &chosen)

	if !slices.Equal(chosen.SwitchedOff, []uint{chosen.Optional}) || !slices.Equal(chosen.Followed, []uint{chosen.Desk}) {
		t.Errorf("the server keeps %v switched off and %v followed; want the notification and the desk",
			chosen.SwitchedOff, chosen.Followed)
	}

	var shown struct {
		Taken    bool `json:"taken"`
		Followed bool `json:"followed"`
	}

	p.evalJSON(`JSON.stringify({
		taken: document.querySelector('#notify-optional input').checked,
		followed: [...document.querySelectorAll('#notify-queues label')]
			.find((label) => label.textContent.includes('Service Desk')).querySelector('input').checked,
	})`, &shown)

	if shown.Taken || !shown.Followed {
		t.Errorf("after saving the form shows the notification taken %v and the desk followed %v",
			shown.Taken, shown.Followed)
	}
}
