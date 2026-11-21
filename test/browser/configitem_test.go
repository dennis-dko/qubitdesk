//go:build browser

package browser

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// An item is added on the Assets screen in a form drawn from its class's
// definition, changed into its next version, and read as it was written in the
// version before; whoever only reads items opens the same form with nothing in
// it to press.
func TestAConfigurationItemIsKeptInVersionsOnTheAssetsScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the assets", p.click(`.tab[data-view="assets"]`),
		chromedp.WaitVisible("#configitem-card", chromedp.ByID))
	p.run("add an item", p.click("#configitem-new"), chromedp.WaitVisible("#configitem-detail", chromedp.ByID))
	p.run("choose the computer", p.chooseOptionByText(`#form-configitem [name="classId"]`, "computer"),
		chromedp.WaitVisible(`#configitem-attributes [data-key="SectionHardware"]`, chromedp.ByQuery))

	// The computer's one required value that has to occur, the day it was first
	// used, is set as a person's choice in the picker would set it.
	p.run("fill it in", chromedp.Evaluate(`(() => {
		const form = document.querySelector('#form-configitem');
		const pick = (select) => {
			select.selectedIndex = 1;
			select.dispatchEvent(new Event('change', { bubbles: true }));
		};
		const write = (input, value) => {
			input.value = value;
			input.dispatchEvent(new Event('input', { bubbles: true }));
		};

		write(form.elements.name, 'Erika laptop');
		pick(form.elements.deploymentStateId);
		pick(form.elements.incidentStateId);
		write(document.querySelector('[data-key="Vendor"] .ci-value'), 'Acme');
		setDateField(document.querySelector('[data-key="FirstUsageDate"] input[type="date"]'), '2026-11-21');
	})()`, nil), p.click("#configitem-submit"))
	p.waitForText("#toast", "Item added")
	p.waitForText("#configitem-form-title", "Item 00000001")

	p.run("change the vendor", chromedp.Evaluate(`(() => {
		const vendor = document.querySelector('[data-key="Vendor"] .ci-value');
		vendor.value = 'Globex';
		vendor.dispatchEvent(new Event('input', { bubbles: true }));
	})()`, nil), p.click("#configitem-submit"))
	p.waitForText("#toast", "Kept as a new version")
	p.waitEvaluates("both versions listed", `String(document.querySelectorAll('#table-configitem-versions tbody tr').length)`,
		"2")

	p.run("read the first version", p.click(`#table-configitem-versions tbody button.link`))
	p.waitForText("#configitem-version-shown", "Version 1 as it was written")

	var older struct {
		Vendor   string `json:"vendor"`
		Disabled bool   `json:"disabled"`
		Saving   bool   `json:"saving"`
		Day      string `json:"day"`
	}

	p.evalJSON(`JSON.stringify({
		vendor: document.querySelector('[data-key="Vendor"] .ci-value').value,
		disabled: document.querySelector('#configitem-fields').disabled,
		saving: !document.querySelector('#configitem-submit').hidden,
		day: document.querySelector('[data-key="FirstUsageDate"] input[type="date"]').value,
	})`, &older)

	if older.Vendor != "Acme" || !older.Disabled || older.Saving || older.Day != "2026-11-21" {
		t.Errorf("the first version reads %+v; want Acme and the first day as written, and nothing to save", older)
	}

	p.becomeWorker()
	p.run("open the assets as an agent", p.click(`.tab[data-view="assets"]`),
		chromedp.WaitVisible("#configitem-card", chromedp.ByID))
	p.run("open the laptop", p.click(`#table-configitem tbody button.link`))
	p.waitForText("#configitem-form-title", "Item 00000001")

	var reader struct {
		Vendor    string   `json:"vendor"`
		Disabled  bool     `json:"disabled"`
		Pressable []string `json:"pressable"`
	}

	p.evalJSON(`JSON.stringify({
		vendor: document.querySelector('[data-key="Vendor"] .ci-value').value,
		disabled: document.querySelector('#configitem-fields').disabled,
		pressable: ['#configitem-new', '#configitem-submit', '#configitem-delete']
			.filter((id) => document.querySelector(id).checkVisibility()),
	})`, &reader)

	if reader.Vendor != "Globex" || !reader.Disabled || len(reader.Pressable) > 0 {
		t.Errorf("an agent opens the laptop as %+v; want Globex, read only, and nothing to press", reader)
	}
}
