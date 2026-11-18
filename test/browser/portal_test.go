//go:build browser

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dennis-dko/go-ticket-system/test/harness"
)

// A customer opens a ticket in the portal and follows it there: given the
// portal from the agents' side, they sign in with the password they were given
// and choose their own, open a ticket on the screen of their tickets - not asked
// for a queue where the portal offers one - find it listed, read the agent's
// answer, and answer the ticket the agent closed, with a file, which opens it
// again.
func TestACustomerOpensAndAnswersATicketInThePortal(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.becomeCustomer()

	p.run("open his tickets", p.click(`.tab[data-view="portal"]`),
		chromedp.WaitVisible("#form-portal-ticket", chromedp.ByID))

	if p.visible("#portal-queue-choice") {
		t.Error("the portal asks for a queue where it offers one")
	}

	p.run("open a ticket",
		chromedp.SetValue(`#form-portal-ticket [name="title"]`, "Screen flickers", chromedp.ByQuery),
		chromedp.SetValue(`#form-portal-ticket [name="body"]`, "Since this morning.", chromedp.ByQuery),
		p.click("#portal-ticket-submit"))
	p.waitForText("#table-portal tbody", "Screen flickers")

	agent := adminSession(t, p)

	var listed struct {
		Items []struct {
			ID    uint   `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}

	p.awaitJSON(`api('/portal/tickets')`, &listed)

	if len(listed.Items) != 1 {
		t.Fatalf("the portal lists %+v; want the one ticket", listed.Items)
	}

	ticket := "/tickets/" + strconv.FormatUint(uint64(listed.Items[0].ID), 10)

	adminCall(t, agent, p.app.BaseURL(), http.MethodPost, ticket+"/articles", map[string]any{
		"body": "Please try another cable.", "customerVisible": true,
	})
	adminCall(t, agent, p.app.BaseURL(), http.MethodPost, ticket+"/articles", map[string]any{
		"body": "Probably the graphics card.",
	})

	var states struct {
		Items []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}

	if err := json.Unmarshal(adminCall(t, agent, p.app.BaseURL(), http.MethodGet, "/ticket-states", nil), &states); err != nil {
		t.Fatal(err)
	}

	for _, state := range states.Items {
		if state.Name == "closed" {
			adminCall(t, agent, p.app.BaseURL(), http.MethodPut, ticket, map[string]any{"stateId": state.ID})
		}
	}

	p.run("open it", p.click(`#table-portal tbody td.text button.link`),
		chromedp.WaitVisible("#portal-ticket", chromedp.ByID))
	p.waitForText("#portal-thread", "Please try another cable.")

	if thread := p.text("#portal-thread"); strings.Contains(thread, "graphics card") {
		t.Errorf("the customer is shown the internal note: %q", thread)
	}

	photo := filepath.Join(t.TempDir(), "photo.txt")
	if err := os.WriteFile(photo, []byte("what the screen shows"), 0o600); err != nil {
		t.Fatal(err)
	}

	p.run("answer it with a file",
		chromedp.SetValue(`#form-portal-answer [name="body"]`, "Another cable did not help.", chromedp.ByQuery),
		chromedp.SetUploadFiles(`#form-portal-answer [name="files"]`, []string{photo}, chromedp.ByQuery),
		p.click("#portal-answer-submit"))
	p.waitForText("#portal-thread", "Another cable did not help.")
	p.waitForText("#portal-thread", "photo.txt")
	p.waitForText("#portal-ticket-facts", "open")
}

// The walk through takes a customer over the portal's screen as well, every step
// wholly on a telephone's screen. The walks of TestEveryTourStepIsWhollyOnScreen
// are an administrator's, who has no portal, so its steps are walked here.
func TestTheTourWalksACustomerThroughThePortal(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.becomeCustomer()

	var portal bool

	p.evalJSON(`JSON.stringify(TOUR_STEPS.some((step) => step.view === 'portal'))`, &portal)

	if !portal {
		t.Fatal("the walk through has no step on the portal's screen")
	}

	p.walkTheTourOn(t, telephones[0], "a customer on "+telephones[0].name)
}

// becomeCustomer makes a contact with the portal as the administrator, and signs
// in as the contact past the initial password, the greeting settled.
func (p *page) becomeCustomer() {
	p.t.Helper()

	p.readyAdmin()

	var carl struct {
		ID uint `json:"id"`
	}

	p.awaitJSON(`api('/contacts', { method: 'POST',
		body: JSON.stringify({ name: 'Carl Customer', email: 'carl@example.com' }) })`, &carl)
	p.awaitJSON(`api('/contacts/`+strconv.FormatUint(uint64(carl.ID), 10)+`/portal-access', { method: 'PUT',
		body: JSON.stringify({ password: 'initial-password-1' }) })`, nil)

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID),
		chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.signIn("carl@example.com", "initial-password-1")
	p.waitGone("#login-screen")
	p.settled()

	p.run("replace the initial password",
		chromedp.Click(`.tab[data-view="settings"]`, chromedp.ByQuery),
		chromedp.WaitVisible("#form-password", chromedp.ByID),
		chromedp.SendKeys(`#form-password input[name="currentPassword"]`, "initial-password-1", chromedp.ByQuery),
		chromedp.SendKeys(`#form-password input[name="newPassword"]`, "carls-own-password", chromedp.ByQuery),
		p.click(`#form-password button[type="submit"]`))
	p.waitGone("#password-banner")
	p.settleWelcome()
}

// adminSession is a second client signed in as the administrator, for what the
// agents do while the browser is signed in as somebody else.
func adminSession(t *testing.T, p *page) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cannot hold the administrator's cookies: %v", err)
	}

	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}

	visit, err := client.Get(p.app.BaseURL())
	if err != nil {
		t.Fatalf("cannot open the page as an administrator would: %v", err)
	}

	_ = visit.Body.Close()

	adminCall(t, client, p.app.BaseURL(), http.MethodPost, "/auth/login", map[string]any{
		"email": harness.AdminEmail, "password": adminPassword,
	})

	return client
}

// adminCall sends one request as the client, with the two headers this API
// insists on, and answers with what the envelope carries as its data.
func adminCall(t *testing.T, client *http.Client, base, method, path string, body any) []byte {
	t.Helper()

	var sent io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("cannot encode the request: %v", err)
		}

		sent = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, base+"/api/v1"+path, sent)
	if err != nil {
		t.Fatalf("cannot build the request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)

	if parsed, err := url.Parse(base); err == nil {
		for _, cookie := range client.Jar.Cookies(parsed) {
			if cookie.Name == "gts_csrf" {
				req.Header.Set("X-CSRF-Token", cookie.Value)
			}
		}
	}

	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() { _ = res.Body.Close() }()

	answer, _ := io.ReadAll(res.Body)

	if res.StatusCode >= http.StatusBadRequest {
		t.Fatalf("%s %s: got %d: %s", method, path, res.StatusCode, answer)
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}

	_ = json.Unmarshal(answer, &envelope)

	return envelope.Data
}
