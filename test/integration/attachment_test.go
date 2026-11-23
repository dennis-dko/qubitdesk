//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"testing"
)

type attachmentResponse struct {
	ID          uint   `json:"id"`
	ArticleID   uint   `json:"articleId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
}

// A file is attached to an article over HTTP, listed with it, and downloaded
// whole as plain bytes whatever it was sent as - and only through its own
// ticket; a request larger than one file is refused before it is read.
func TestAFileIsAttachedListedAndDownloadedOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	open := func(title string) uint {
		var ticket ticketResponse

		admin.must(admin.api(http.MethodPost, "/tickets", map[string]any{
			"title": title, "queueId": desk, "body": "It jams.",
		}), http.StatusCreated).Data(t, &ticket)

		return ticket.ID
	}

	printer := open("Printer")

	var articles struct {
		Items []struct {
			ID          uint                 `json:"id"`
			Attachments []attachmentResponse `json:"attachments"`
		} `json:"items"`
	}

	admin.must(admin.api(http.MethodGet, ticketPath(printer, "articles"), nil), http.StatusOK).Data(t, &articles)

	if len(articles.Items) == 0 || articles.Items[0].Attachments == nil {
		t.Fatalf("the articles read as %+v; want the first with an empty list of files", articles.Items)
	}

	first := articles.Items[0].ID
	content := []byte("<html><script>alert(1)</script></html>")

	var kept attachmentResponse

	admin.must(uploadTyped(admin, ticketPath(printer, "articles", strconv.FormatUint(uint64(first), 10), "attachments"),
		"page.html", "text/html", content), http.StatusCreated).Data(t, &kept)

	if kept.Filename != "page.html" || kept.ContentType != "text/html" || kept.Size != len(content) || kept.ArticleID != first {
		t.Errorf("the file was kept as %+v", kept)
	}

	admin.must(admin.api(http.MethodGet, ticketPath(printer, "articles"), nil), http.StatusOK).Data(t, &articles)

	if files := articles.Items[0].Attachments; len(files) != 1 || files[0].ID != kept.ID {
		t.Errorf("the first article lists %+v; want the file just attached", files)
	}

	download := admin.api(http.MethodGet, ticketPath(printer, "attachments", strconv.FormatUint(uint64(kept.ID), 10)), nil)
	if download.Status != http.StatusOK || !bytes.Equal(download.Body, content) {
		t.Fatalf("downloading the file was answered %d with %q", download.Status, download.Body)
	}

	if got := download.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("a page somebody uploaded was served as %q; it may only ever go back as bytes", got)
	}

	scanner := open("Scanner")
	elsewhere := admin.api(http.MethodGet, ticketPath(scanner, "attachments", strconv.FormatUint(uint64(kept.ID), 10)), nil)

	if elsewhere.Status != http.StatusNotFound {
		t.Errorf("the file read through another ticket was answered %d, want 404", elsewhere.Status)
	}

	tooLarge := admin.upload(ticketPath(printer, "articles", strconv.FormatUint(uint64(first), 10), "attachments"),
		"file", "big.bin", make([]byte, 21<<20), nil)
	if tooLarge.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("a request of 21 MB was answered %d, want 413", tooLarge.Status)
	}

	// Its files go with a ticket, before the articles they hang from.
	admin.must(admin.api(http.MethodDelete, ticketPath(printer), nil), http.StatusNoContent, http.StatusOK)
}

// uploadTyped uploads one file in the field "file" under the type it declares,
// which upload - through CreateFormFile - always sends as plain bytes, so a case
// asking what becomes of a declared type could not tell.
func uploadTyped(c *client, path, filename, contentType string, content []byte) response {
	c.t.Helper()

	var body bytes.Buffer

	form := multipart.NewWriter(&body)

	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", contentType)

	part, err := form.CreatePart(header)
	if err != nil {
		c.t.Fatalf("cannot build the upload: %v", err)
	}

	if _, err := part.Write(content); err != nil {
		c.t.Fatalf("cannot write the upload: %v", err)
	}

	if err := form.Close(); err != nil {
		c.t.Fatalf("cannot close the upload: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		c.app.BaseURL()+"/api/v1"+path, &body)
	if err != nil {
		c.t.Fatalf("cannot build the request: %v", err)
	}

	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Origin", c.app.BaseURL())
	req.Header.Set("X-CSRF-Token", c.csrfToken())

	return c.send(req, http.MethodPost, path)
}
