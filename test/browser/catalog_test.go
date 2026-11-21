//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// The general catalog is kept on its screen: the deployment states that ship
// are named in the reader's words, each with what it means; a value is added to
// a list with the functionality the list asks for; a list asking for none puts
// the question away, and a class of its own is made by adding its first value;
// and a value is retired from its row rather than deleted.
func TestTheGeneralCatalogIsKeptOnItsScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the assets", p.click(`.tab[data-view="assets"]`),
		chromedp.WaitVisible("#form-catalog", chromedp.ByID))
	p.run("choose the deployment states",
		p.chooseOption(`#form-catalog-class [name="class"]`, "ITSM::ConfigItem::DeploymentState"))
	p.waitForText("#table-catalog tbody", "planned")

	if rows := p.text("#table-catalog tbody"); !strings.Contains(rows, "before use") ||
		strings.Contains(rows, "test-qa") {
		t.Errorf("the deployment states read %q; want them and what each means in words", rows)
	}

	p.run("add staging",
		chromedp.SetValue(`#form-catalog [name="name"]`, "staging", chromedp.ByQuery),
		p.chooseOption(`#form-catalog [name="functionality"]`, "preproductive"),
		p.click("#catalog-submit"))
	p.waitForText("#table-catalog tbody", "staging")

	p.run("start a list of its own",
		chromedp.SetValue(`#form-catalog [name="class"]`, "ITSM::ConfigItem::Laptop::Brand", chromedp.ByQuery))

	if p.visible("#catalog-functionality") {
		t.Error("a list whose values mean nothing to the code still asks what a value means")
	}

	p.run("add its first value",
		chromedp.SetValue(`#form-catalog [name="name"]`, "Acme", chromedp.ByQuery),
		p.click("#catalog-submit"))
	p.waitForText("#table-catalog tbody", "Acme")

	p.run("correct Acme", p.click(`#table-catalog tbody td.actions button.link`))
	p.waitForText("#catalog-form-title", "Acme")
	p.run("retire it", chromedp.Click(`#form-catalog [name="valid"]`, chromedp.ByQuery), p.click("#catalog-submit"))
	p.waitForText("#toast", "Value saved")
	p.waitForText("#table-catalog tbody", "no")
}
