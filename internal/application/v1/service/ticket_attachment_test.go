package service_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// A file is kept with an article of a ticket the caller works on, under a plain
// name, written into the history and read back whole - and only through its own
// ticket. An article of another ticket, a file of another ticket, a queue the
// caller only reads or does not reach, an empty file and one past the bound are
// refused.
func TestAFileIsKeptWithAnArticleAndReachedThroughItsTicket(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	ticket := f.open(t, f.agent, f.desk, "Printer")

	articles, err := f.tickets.Articles(ctx, f.agent, ticket.ID)
	if err != nil || len(articles) == 0 {
		t.Fatalf("the ticket's articles: %v, %v", articles, err)
	}

	first := articles[0].ID
	png := service.AttachedFile{Name: `C:\scans\jam.png`, Type: "image/png", Content: []byte("png bytes")}

	kept, err := f.tickets.AddAttachment(ctx, f.agent, ticket.ID, first, png)
	if err != nil {
		t.Fatal(err)
	}

	if kept.Filename != "jam.png" || kept.ContentType != "image/png" || kept.Size != len(png.Content) || kept.Content != nil {
		t.Errorf("the file was kept as %+v", kept)
	}

	history, err := f.tickets.History(ctx, f.agent, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}

	if last := history[len(history)-1]; last.Event != model.HistoryAttachment || last.NewValue != "jam.png" {
		t.Errorf("the history ends with %+v; want the file attached", last)
	}

	read, err := f.tickets.Attachment(ctx, f.agent, ticket.ID, kept.ID)
	if err != nil || string(read.Content) != "png bytes" {
		t.Fatalf("reading the file back answered %+v, %v", read, err)
	}

	listed, err := f.tickets.Attachments(ctx, f.agent, ticket.ID)
	if err != nil || len(listed) != 1 || listed[0].Content != nil {
		t.Errorf("the ticket's files are listed as %+v, %v; want the one, without its content", listed, err)
	}

	other := f.open(t, f.agent, f.desk, "Scanner")
	readOnly := f.open(t, f.admin, f.readOnly, "Invoice")
	hidden := f.open(t, f.admin, f.hidden, "Board minutes")

	for _, refused := range []struct {
		what string
		err  error
		code string
	}{
		{"a file read through another ticket", errOf(f.tickets.Attachment(ctx, f.agent, other.ID, kept.ID)), ""},
		{"a file kept with another ticket's article", errOf(f.tickets.AddAttachment(ctx, f.agent, other.ID, first, png)), ""},
		{"a file kept in a queue only read", errOf(f.tickets.AddAttachment(ctx, f.agent, readOnly.ID, first, png)), ""},
		{"the files of a ticket out of reach", errOf(f.tickets.Attachments(ctx, f.agent, hidden.ID)), ""},
		{"an empty file", errOf(f.tickets.AddAttachment(ctx, f.agent, ticket.ID, first,
			service.AttachedFile{Name: "empty.txt"})), "noFileUploaded"},
		{"a file past the bound", errOf(f.tickets.AddAttachment(ctx, f.agent, ticket.ID, first,
			service.AttachedFile{Name: "big.bin", Content: make([]byte, model.MaxAttachmentBytes+1)})), "attachmentTooLarge"},
	} {
		if refused.code == "" {
			if apperror.KindOf(refused.err) != apperror.KindNotFound {
				t.Errorf("%s was answered %v, want not found", refused.what, refused.err)
			}

			continue
		}

		if detail, ok := apperror.Detail(refused.err); !ok || detail.Code != refused.code {
			t.Errorf("%s was answered %v, want %s", refused.what, refused.err, refused.code)
		}
	}
}

// errOf is the error of a call whose value the case does not need.
func errOf[T any](_ T, err error) error { return err }
