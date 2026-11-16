//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

type calendarSeen struct {
	Items []struct {
		Name         string `json:"name"`
		HolidayPlace string `json:"holidayPlace"`
		Hours        []struct {
			Weekday int    `json:"weekday"`
			From    string `json:"from"`
			To      string `json:"to"`
		} `json:"hours"`
		FreeDays []struct {
			Day       string `json:"day"`
			Recurring bool   `json:"recurring"`
			Name      string `json:"name"`
		} `json:"freeDays"`
	} `json:"items"`
}

// A calendar is made on the ticket setup screen - a weekday with a break, a
// free day every year, no public holidays - and corrected there, the hours
// replaced and the free day kept; hours that cannot be read are marked rather
// than sent.
func TestACalendarIsMadeAndCorrectedOnTheSetupScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	// The weekdays' boxes are drawn once the calendars have been read.
	p.run("open the setup", p.click(`.tab[data-view="ticketsetup"]`),
		chromedp.WaitVisible(`#form-calendar [name="hours1"]`, chromedp.ByQuery))

	p.run("describe the calendar",
		chromedp.SetValue(`#form-calendar [name="name"]`, "Service hours", chromedp.ByQuery),
		p.chooseOption(`#form-calendar [name="country"]`, "none"),
		chromedp.SetValue(`#form-calendar [name="hours1"]`, "08:00–12:00, 13:00–17:00", chromedp.ByQuery),
		chromedp.SetValue(`#form-calendar [name="hours2"]`, "9-17", chromedp.ByQuery),
		p.click("#calendar-submit"))

	var marked string

	p.run("read what the Tuesday box says", chromedp.Evaluate(
		`document.querySelector('#form-calendar [name="hours2"]').validationMessage`, &marked))

	if marked == "" {
		t.Error("hours that cannot be read were not marked")
	}

	var seen calendarSeen

	p.awaitJSON(`api('/calendars')`, &seen)

	if len(seen.Items) != 0 {
		t.Fatalf("a calendar with hours that cannot be read was sent: %+v", seen.Items)
	}

	// Emptied from the page: chromedp's SetValue with nothing writes "undefined".
	p.run("clear the Tuesday and add a free day",
		chromedp.Evaluate(`(() => {
			const box = document.querySelector('#form-calendar [name="hours2"]');
			box.value = '';
			box.dispatchEvent(new Event('input', { bubbles: true }));
		})()`, nil),
		p.click("#calendar-add-day"),
		chromedp.WaitVisible(".calendar-day", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const row = document.querySelector('.calendar-day');
			setDateField(row.querySelector('input[name="freeDay"]'), '2026-12-24');
			row.querySelector('input[name="freeDayRecurring"]').checked = true;
			row.querySelector('input[name="freeDayName"]').value = 'Christmas Eve';
		})()`, nil),
		p.click("#calendar-submit"))
	p.waitForText("#table-calendar tbody", "Service hours")

	p.awaitJSON(`api('/calendars')`, &seen)

	if len(seen.Items) != 1 || seen.Items[0].HolidayPlace != "none" || len(seen.Items[0].Hours) != 2 ||
		seen.Items[0].Hours[1].From != "13:00" || len(seen.Items[0].FreeDays) != 1 ||
		seen.Items[0].FreeDays[0].Day != "2026-12-24" || !seen.Items[0].FreeDays[0].Recurring {
		t.Fatalf("the calendar was kept as %+v", seen.Items)
	}

	p.run("correct it", chromedp.Evaluate(`[...document.querySelectorAll('#table-calendar tbody tr')]
		.find((row) => row.textContent.includes('Service hours')).querySelector('button.link:not(.danger)').click()`, nil))
	p.waitForText("#calendar-form-title", "Service hours")

	if monday := p.value(`#form-calendar [name="hours1"]`); !strings.Contains(monday, "13:00–17:00") {
		t.Errorf("the Monday box reads %q while the calendar is corrected", monday)
	}

	p.run("change the Monday",
		chromedp.SetValue(`#form-calendar [name="hours1"]`, "09:00–17:00", chromedp.ByQuery),
		p.click("#calendar-submit"))
	p.waitForText("#calendar-form-title", "Add a calendar")

	p.awaitJSON(`api('/calendars')`, &seen)

	if len(seen.Items) != 1 || len(seen.Items[0].Hours) != 1 || seen.Items[0].Hours[0].From != "09:00" ||
		len(seen.Items[0].FreeDays) != 1 {
		t.Errorf("after the correction the calendar reads %+v", seen.Items)
	}
}
