//go:build browser

package browser

import (
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// linesOfWords is a script function counting the lines an element's words are
// laid out on: one per distinct line box, read off the words themselves. Not
// the element's height over its line height, which a padded button or a cell as
// tall as its row turns into a wrong answer.
const linesOfWords = `(element) => {
	const tops = [];
	const words = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);

	for (let node = words.nextNode(); node; node = words.nextNode()) {
		if (!node.textContent.trim()) continue;

		const range = document.createRange();
		range.selectNodeContents(node);

		for (const line of range.getClientRects()) {
			if (line.width > 0 && !tops.some((top) => Math.abs(top - line.top) < line.height / 2)) {
				tops.push(line.top);
			}
		}
	}

	return tops.length;
}`

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}

	return s[:max] + "…"
}

// waitForFilled waits until an element has something written in it.
//
// For the screens whose cards are in the markup and whose contents arrive by
// request: the card is there from the first paint, and only what is in it says
// whether the answer has come back.
func (p *page) waitForFilled(selector string) {
	p.t.Helper()

	deadline := time.Now().Add(waitPatience)

	for time.Now().Before(deadline) {
		if strings.TrimSpace(p.text(selector)) != "" {
			return
		}

		time.Sleep(250 * time.Millisecond)
	}

	p.t.Fatalf("%s was never written to, so the screen stopped loading before it;"+
		" the log says:\n%s", selector, p.app.Log())
}

// waitForValue waits until a field has something in it.
//
// For the screens whose forms are in the markup and whose contents arrive by
// request: the field is there from the first paint, and only what is in it says
// whether the answer has come back.
func (p *page) waitForValue(selector string) {
	p.t.Helper()

	deadline := time.Now().Add(waitPatience)

	for time.Now().Before(deadline) {
		if p.value(selector) != "" {
			return
		}

		time.Sleep(250 * time.Millisecond)
	}

	p.t.Fatalf("%s was never filled in, so the screen stopped loading before it;"+
		" the log says:\n%s", selector, p.app.Log())
}

// waitForNode waits until something matches.
func (p *page) waitForNode(selector string) {
	p.t.Helper()

	deadline := time.Now().Add(waitPatience)

	for time.Now().Before(deadline) {
		if p.count(selector) > 0 {
			return
		}

		time.Sleep(200 * time.Millisecond)
	}

	p.t.Fatalf("nothing ever matched %s", selector)
}

// submitAndAwaitSave sends the appearance form and waits for the save to land.
//
// On the same document: saving the appearance redraws the page rather than
// reloading it, so a reload here is a failure rather than something to wait for.
// The mark left on the document is what tells the two apart.
func (p *page) submitAndAwaitSave(t *testing.T, submit chromedp.Action) {
	t.Helper()

	p.run("watch for the answer", chromedp.Evaluate(`
		(() => {
			window.__beforeSave = true;
			window.__saved = 'no answer';

			const real = window.fetch;

			window.fetch = async (url, options) => {
				const answer = await real(url, options);

				if (String(url).includes('/settings/branding') && options?.method === 'PUT') {
					window.__saved = String(answer.status);
				}

				return answer;
			};

			return 1;
		})()`, nil))

	p.run("save", submit)
	p.waitEvaluates("the save's answer", `String(window.__saved)`, "200")

	var kept bool

	p.run("ask whether the document stayed", chromedp.Evaluate(`window.__beforeSave === true`, &kept))

	if !kept {
		t.Fatal("saving the appearance reloaded the page")
	}
}
