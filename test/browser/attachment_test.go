//go:build browser

package browser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

// A file chosen with an article is named beside the chooser, attached once the
// article is written, listed under it, and saved under the name it came with -
// not the dated name an export gets.
func TestAFileChosenWithAnArticleIsListedAndSavedUnderItsName(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	report := filepath.Join(t.TempDir(), "jam report.txt")
	if err := os.WriteFile(report, []byte("The roller is worn."), 0o600); err != nil {
		t.Fatal(err)
	}

	var made bool

	p.awaitJSON(`(async () => {
		const queue = (await api('/queues')).items[0];
		await api('/tickets', { method: 'POST', body: JSON.stringify(
			{ title: 'Printer', queueId: queue.id, body: 'It jams.' }) });
		await refreshAll();
		return true;
	})()`, &made)

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitForText("#table-tickets tbody", "Printer")
	p.run("open the printer", chromedp.Evaluate(`[...document.querySelectorAll('#table-tickets tbody tr')]
		.find((row) => row.textContent.includes('Printer')).querySelector('button.link').click()`, nil),
		chromedp.WaitVisible("#ticket-detail", chromedp.ByID))

	p.run("choose the report", chromedp.SetUploadFiles(`#form-article [name="files"]`, []string{report}, chromedp.ByQuery))
	p.waitForText("#article-files", "jam report.txt")

	p.run("write in it",
		chromedp.SetValue(`#form-article [name="body"]`, "The report is attached.", chromedp.ByQuery),
		p.click(`#form-article button[type="submit"]`))
	p.waitForText("#ticket-articles", "jam report.txt")

	if left := p.text("#article-files"); left != "" {
		t.Errorf("after the article was written the form still names %q", left)
	}

	into := t.TempDir()

	p.run("take downloads here", browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
		WithDownloadPath(filepath.FromSlash(into)).WithEventsEnabled(true))
	p.run("save the report", chromedp.Evaluate(`[...document.querySelectorAll('.ticket-attachments button')]
		.find((button) => button.textContent.startsWith('jam report.txt')).click()`, nil))

	saved := waitForDownload(t, into, ".txt")

	if name := filepath.Base(saved); name != "jam report.txt" {
		t.Errorf("the report was saved as %q, want the name it came with", name)
	}

	if content, err := os.ReadFile(saved); err != nil || string(content) != "The roller is worn." {
		t.Errorf("the saved report reads %q, %v", content, err)
	}
}
