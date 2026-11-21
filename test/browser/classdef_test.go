//go:build browser

package browser

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// A class's definition is written on the Assets screen: the version standing
// now is in the box, a change is kept as the next version and listed beside the
// one before it, and what is not JSON is said on the screen before anything is
// sent.
func TestAClassDefinitionIsWrittenOnTheAssetsScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the assets", p.click(`.tab[data-view="assets"]`),
		chromedp.WaitVisible("#form-classdef", chromedp.ByID))
	p.run("choose the computer",
		p.chooseOptionByText(`#form-classdef [name="classId"]`, "computer"))
	p.waitForText("#classdef-version", "Version 1")

	var shipped bool

	p.evalJSON(`JSON.stringify(document.querySelector('#form-classdef [name="attributes"]').value
		.includes('"SectionGeneral"'))`, &shipped)

	if !shipped {
		t.Fatal("the box does not hold the computer's shipped definition")
	}

	p.run("write something that is not JSON", chromedp.Evaluate(`(() => {
		const box = document.querySelector('#form-classdef [name="attributes"]');
		box.value = '[{"key": ';
		box.dispatchEvent(new Event('input', { bubbles: true }));
	})()`, nil), p.click("#classdef-submit"))
	p.waitForText("#toast", "The definition is not JSON.")

	p.run("add an attribute", chromedp.Evaluate(`(async () => {
		const versions = await api('/cmdb/classes/' + document.querySelector('#form-classdef [name="classId"]').value
			+ '/definitions');
		const attributes = versions.items[0].attributes;
		attributes.push({ key: 'AssetTag', name: 'Asset tag', type: 'Text', countMin: 0, countMax: 1, countDefault: 1 });
		const box = document.querySelector('#form-classdef [name="attributes"]');
		box.value = JSON.stringify(attributes, null, 2);
		box.dispatchEvent(new Event('input', { bubbles: true }));
	})()`, nil, awaitPromise), p.click("#classdef-submit"))
	p.waitForText("#toast", "Kept as a new version")
	p.waitForText("#classdef-version", "Version 2")
	p.waitForText("#table-classdef-versions tbody", "2")

	var rows int

	p.evalJSON(`JSON.stringify(document.querySelectorAll('#table-classdef-versions tbody tr').length)`, &rows)

	if rows != 2 {
		t.Errorf("the versions table lists %d rows after the change, want the new version and the shipped one", rows)
	}
}
