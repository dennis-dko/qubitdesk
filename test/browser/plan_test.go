//go:build browser

package browser

import (
	"slices"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// planKept is an execution plan as the API hands it out.
type planKept struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Weekdays []int    `json:"weekdays"`
	Times    []string `json:"times"`
	Events   []string `json:"events"`
}

// An execution plan is made on the automation screen to run at times of day on
// the weekdays it names, or on what happens to a ticket. The form shows what the
// plan's kind reads and puts the other kind's away - a plan made for events is
// not held back by the times it has no use for - and a plan changed from one
// kind to the other keeps nothing of the kind it was.
func TestAnExecutionPlanRunsAtTimesOrOnEvents(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the automation", p.click(`.tab[data-view="automation"]`),
		chromedp.WaitVisible("#form-plan", chromedp.ByID))

	if p.visible("#plan-event") {
		t.Error("a new plan, which runs at times, offers the events")
	}

	p.run("make Mornings",
		chromedp.SetValue(`#form-plan [name="name"]`, "Mornings", chromedp.ByQuery),
		chromedp.Evaluate(`for (const day of [1, 2, 3, 4, 5]) {
			document.querySelector('#plan-weekdays [data-weekday="' + day + '"]').click();
		}`, nil),
		chromedp.SetValue(`#form-plan [name="times"]`, "12:30, 08:00", chromedp.ByQuery),
		p.click("#plan-submit"))
	p.waitForText("#table-plan tbody", "Mornings")

	if row := p.text("#table-plan tbody"); !strings.Contains(row, "Mon, Tue, Wed, Thu, Fri at 08:00, 12:30") {
		t.Errorf("the plan's row reads %q; want its weekdays and its times in order", row)
	}

	p.run("make one for openings",
		chromedp.SetValue(`#form-plan [name="name"]`, "On opening", chromedp.ByQuery),
		p.chooseOption(`#form-plan [name="type"]`, "EventBased"))

	if p.visible("#plan-time") || !p.visible("#plan-event") {
		t.Fatal("a plan switched to events still shows the times, or not the events")
	}

	p.run("that waits for a ticket to be opened",
		chromedp.Evaluate(`document.querySelector('#plan-events [data-event="TicketCreate"]').click()`, nil),
		p.click("#plan-submit"))
	p.waitForText("#table-plan tbody", "On opening")

	p.run("open Mornings again", chromedp.Evaluate(`[...document.querySelectorAll('#table-plan tbody tr')]
		.find((row) => row.textContent.includes('Mornings')).querySelector('button.link:not(.danger)').click()`, nil))
	p.waitForText("#plan-form-title", "Mornings")

	var ticked int

	p.evalJSON(`JSON.stringify(document.querySelectorAll('#plan-weekdays input:checked').length)`, &ticked)

	if ticked != 5 || p.value(`#form-plan [name="times"]`) != "08:00, 12:30" {
		t.Errorf("the opened plan shows %d weekdays and the times %q", ticked, p.value(`#form-plan [name="times"]`))
	}

	p.run("run it on events instead",
		p.chooseOption(`#form-plan [name="type"]`, "EventBased"),
		chromedp.Evaluate(`for (const event of ['TicketCreate', 'TicketStateUpdate']) {
			document.querySelector('#plan-events [data-event="' + event + '"]').click();
		}`, nil),
		p.click("#plan-submit"))
	p.waitForText("#table-plan tbody", "Ticket opened, State changed")

	var plans []planKept

	p.awaitJSON(`(async () => (await api('/exec-plans')).items)()`, &plans)

	want := map[string]planKept{
		"Mornings":   {Type: "EventBased", Events: []string{"TicketCreate", "TicketStateUpdate"}},
		"On opening": {Type: "EventBased", Events: []string{"TicketCreate"}},
	}

	for _, plan := range plans {
		wanted, known := want[plan.Name]
		if !known {
			continue
		}

		delete(want, plan.Name)

		if plan.Type != wanted.Type || !slices.Equal(plan.Events, wanted.Events) ||
			len(plan.Weekdays) != 0 || len(plan.Times) != 0 {
			t.Errorf("the server keeps %s as %+v, want %+v and no weekdays or times", plan.Name, plan, wanted)
		}
	}

	for name := range want {
		t.Errorf("the server keeps no plan %s", name)
	}
}
