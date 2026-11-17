//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// macroStep is a step of a macro as the API hands it out.
type macroStep struct {
	Type       string            `json:"type"`
	Parameters map[string]string `json:"parameters"`
}

// A macro is put together on the automation screen a step at a time: each step
// asks for what its kind reads, moves and goes when it is told to, and what the
// server keeps is what the form showed, in the order it showed it. Opened again,
// the form shows the steps as they were saved, and saving it sends them back
// unchanged.
func TestAMacroIsPutTogetherStepByStep(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the automation", p.click(`.tab[data-view="automation"]`),
		chromedp.WaitVisible("#form-macro", chromedp.ByID))

	p.run("name it", chromedp.SetValue(`#form-macro [name="name"]`, "Escalate", chromedp.ByQuery))
	p.run("raise the priority",
		p.chooseOptionByText("#macro-step-kind", "Set the priority"), p.click("#macro-add-step"),
		p.chooseOptionByText(`#macro-steps > li:nth-child(1) [data-parameter="priorityId"]`, "very high"))
	p.run("say so",
		p.chooseOptionByText("#macro-step-kind", "Add an article"), p.click("#macro-add-step"),
		chromedp.SetValue(`#macro-steps > li:nth-child(2) [data-parameter="body"]`, "Escalated by the desk.",
			chromedp.ByQuery))
	p.run("and delete it",
		p.chooseOptionByText("#macro-step-kind", "Delete the ticket"), p.click("#macro-add-step"))

	p.run("say so first", p.click(`#macro-steps > li:nth-child(2) .ordered-up`))
	p.run("delete nothing after all", p.click(`#macro-steps > li:nth-child(3) .ordered-remove`))

	if got := p.count("#macro-steps > li"); got != 2 {
		t.Fatalf("the form holds %d steps after one was taken out of three", got)
	}

	p.run("save it", p.click("#macro-submit"))
	p.waitForText("#table-macro tbody", "Escalate")

	if row := p.text("#table-macro tbody"); !strings.Contains(row, "Add an article, Set the priority") {
		t.Errorf("the macro's row reads %q; want its two steps in the order they were left in", row)
	}

	var saved struct {
		Steps    []macroStep `json:"steps"`
		VeryHigh string      `json:"veryHigh"`
	}

	read := `(async () => {
		const macro = (await api('/macros')).items.find((m) => m.name.startsWith('Escalate'));
		const priorities = (await api('/ticket-priorities')).items;
		return { steps: macro.actions, veryHigh: String(priorities.find((p) => p.name === 'very-high').id) };
	})()`

	p.awaitJSON(read, &saved)

	want := []macroStep{
		{Type: "ArticleCreate", Parameters: map[string]string{
			"channel": "note", "body": "Escalated by the desk.", "customerVisible": "false",
		}},
		{Type: "PrioritySet", Parameters: map[string]string{"priorityId": saved.VeryHigh}},
	}

	if !sameSteps(saved.Steps, want) {
		t.Fatalf("the server keeps the steps %+v, want %+v", saved.Steps, want)
	}

	p.run("open it again", chromedp.Evaluate(`[...document.querySelectorAll('#table-macro tbody tr')]
		.find((row) => row.textContent.includes('Escalate')).querySelector('button.link:not(.danger)').click()`, nil))
	p.waitForText("#macro-form-title", "Escalate")

	if got := p.value(`#macro-steps > li:nth-child(2) [data-parameter="priorityId"]`); got != saved.VeryHigh {
		t.Errorf("the opened macro's priority step names %q, want %q", got, saved.VeryHigh)
	}

	if got := p.value(`#macro-steps > li:nth-child(1) [data-parameter="body"]`); got != "Escalated by the desk." {
		t.Errorf("the opened macro's article says %q", got)
	}

	p.run("rename it",
		chromedp.SetValue(`#form-macro [name="name"]`, "Escalate now", chromedp.ByQuery),
		p.click("#macro-submit"))
	p.waitForText("#table-macro tbody", "Escalate now")
	p.waitForText("#macro-form-title", "Add a macro")

	p.awaitJSON(read, &saved)

	if !sameSteps(saved.Steps, want) {
		t.Errorf("saving the opened macro changed its steps to %+v", saved.Steps)
	}
}

// A step setting a state asks how long the ticket is to wait beside a state that
// waits, and asks for nothing beside one that does not - the server wants the
// minutes for the one and refuses them for the other.
func TestAStepSettingAWaitingStateAsksHowLongItWaits(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the automation", p.click(`.tab[data-view="automation"]`),
		chromedp.WaitVisible("#form-macro", chromedp.ByID))

	const state = `#macro-steps > li [data-parameter="stateId"]`

	const minutes = `#macro-steps > li [data-parameter="pendingMinutes"]`

	p.run("name it", chromedp.SetValue(`#form-macro [name="name"]`, "Park", chromedp.ByQuery))
	p.run("set a state", p.chooseOptionByText("#macro-step-kind", "Set the state"), p.click("#macro-add-step"))

	if p.visible(minutes) {
		t.Error("a step with no state chosen yet asks how long the ticket waits")
	}

	p.run("open it", p.chooseOptionByText(state, "open"))

	if p.visible(minutes) {
		t.Error("a step setting a state that waits for nothing asks how long the ticket waits")
	}

	p.run("park it instead", p.chooseOptionByText(state, "pending reminder"))

	if !p.visible(minutes) {
		t.Fatal("a step setting a waiting state does not ask how long the ticket waits")
	}

	var required bool

	p.evalJSON(`JSON.stringify(document.querySelector('`+minutes+`').required)`, &required)

	if !required {
		t.Error("the minutes a waiting state waits for can be left out, which the server refuses")
	}

	p.run("for an hour and a half", chromedp.SetValue(minutes, "90", chromedp.ByQuery), p.click("#macro-submit"))
	p.waitForText("#table-macro tbody", "Park")

	var saved struct {
		Steps   []macroStep `json:"steps"`
		Waiting string      `json:"waiting"`
	}

	p.awaitJSON(`(async () => {
		const macro = (await api('/macros')).items.find((m) => m.name === 'Park');
		const states = (await api('/ticket-states')).items;
		return { steps: macro.actions, waiting: String(states.find((s) => s.name === 'pending-reminder').id) };
	})()`, &saved)

	want := []macroStep{{Type: "StateSet", Parameters: map[string]string{
		"stateId": saved.Waiting, "pendingMinutes": "90",
	}}}

	if !sameSteps(saved.Steps, want) {
		t.Errorf("the server keeps the steps %+v, want %+v", saved.Steps, want)
	}
}

// A macro naming a state that has since been retired opens with that state still
// chosen rather than quietly swapped for another, and saving it is refused with
// the step and the field named in the reader's words - "Status", which is what
// the box is labelled, not "stateId", which is how the field travels.
func TestAMacroNamingARetiredStateIsRefusedInTheReadersWords(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const state = await api('/ticket-states', { method: 'POST',
			body: JSON.stringify({ name: 'Waiting for parts', type: 'open' }) });
		await api('/macros', { method: 'POST', body: JSON.stringify({ name: 'Park for parts',
			actions: [{ type: 'StateSet', parameters: { stateId: String(state.id) } }] }) });
		await api('/ticket-states/' + state.id, { method: 'PUT', body: JSON.stringify({ valid: false }) });
		await refreshAll();
		return true;
	})()`, &made)

	if !made {
		t.Fatal("could not make the macro and retire its state")
	}

	p.chooseLanguage("de")

	p.run("open the automation", p.click(`.tab[data-view="automation"]`),
		chromedp.WaitVisible("#form-macro", chromedp.ByID))
	p.waitForText("#table-macro tbody", "Park for parts")
	p.run("open the macro", chromedp.Evaluate(`[...document.querySelectorAll('#table-macro tbody tr')]
		.find((row) => row.textContent.includes('Park for parts')).querySelector('button.link:not(.danger)').click()`, nil))
	p.waitForText("#macro-form-title", "Park for parts")

	var chosen string

	p.evalJSON(`JSON.stringify(document.querySelector('#macro-steps > li [data-parameter="stateId"]')
		.selectedOptions[0]?.textContent ?? '')`, &chosen)

	if chosen != "Waiting for parts" {
		t.Errorf("the opened macro's state step shows %q, want the retired state it names", chosen)
	}

	p.run("save it", p.click("#macro-submit"))
	p.waitForText("#toast", "Schritt 1 des Makros kann so nicht ausgeführt werden: „Status“ ist nicht gültig.")
}

// sameSteps says whether two runs of steps are the same steps in the same order.
func sameSteps(got, want []macroStep) bool {
	if len(got) != len(want) {
		return false
	}

	for i := range want {
		if got[i].Type != want[i].Type || len(got[i].Parameters) != len(want[i].Parameters) {
			return false
		}

		for name, value := range want[i].Parameters {
			if got[i].Parameters[name] != value {
				return false
			}
		}
	}

	return true
}
