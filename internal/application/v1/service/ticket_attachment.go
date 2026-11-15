package service

import (
	"context"
	"strconv"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// AddAttachment keeps a file with an article of a ticket the caller works on.
// The file is bounded here rather than only where it arrives, because it will
// arrive by more than one road: an upload now, a mail later.
func (s *TicketApplicationService) AddAttachment(
	ctx context.Context,
	caller *Principal,
	ticketID, articleID uint,
	file AttachedFile,
) (*model.Attachment, error) {
	ticket, _, err := s.reached(ctx, caller, ticketID, true)
	if err != nil {
		return nil, err
	}

	if err := s.articleOf(ctx, ticket.ID, articleID); err != nil {
		return nil, err
	}

	if len(file.Content) == 0 {
		return nil, apperror.Invalidf("no file was uploaded").WithCode("noFileUploaded")
	}

	if len(file.Content) > model.MaxAttachmentBytes {
		return nil, apperror.Invalidf("a file may be at most %d MB", model.MaxAttachmentBytes>>20).
			WithCode("attachmentTooLarge", model.MaxAttachmentBytes>>20)
	}

	now := s.now().UTC()
	attachment := &model.Attachment{
		ArticleID: articleID, TicketID: ticket.ID,
		Filename: model.AttachmentName(file.Name), ContentType: model.AttachmentType(file.Type),
		Size: len(file.Content), Content: file.Content,
	}

	change := historyOf(ticket.ID, now, caller)
	change.add(model.HistoryAttachment, "", attachment.Filename)

	return s.tickets.AddAttachment(ctx, attachment, now, change.entries)
}

// AttachedFile is a file as it arrived, with the name and the type it was sent
// under, which AddAttachment makes plain before it keeps them.
type AttachedFile struct {
	Name    string
	Type    string
	Content []byte
}

// Attachments lists the files of a ticket the caller reaches, without content.
func (s *TicketApplicationService) Attachments(
	ctx context.Context,
	caller *Principal,
	ticketID uint,
) ([]*model.Attachment, error) {
	if _, _, err := s.reached(ctx, caller, ticketID, false); err != nil {
		return nil, err
	}

	return s.tickets.Attachments(ctx, ticketID)
}

// Attachment is one file of a ticket the caller reaches, with its content. A
// file of another ticket is not found, so that a ticket the caller reaches is no
// window onto one they do not.
func (s *TicketApplicationService) Attachment(
	ctx context.Context,
	caller *Principal,
	ticketID, attachmentID uint,
) (*model.Attachment, error) {
	if _, _, err := s.reached(ctx, caller, ticketID, false); err != nil {
		return nil, err
	}

	attachment, err := s.tickets.Attachment(ctx, attachmentID)
	if err != nil {
		return nil, err
	}

	if attachment.TicketID != ticketID {
		return nil, apperror.NotFound("attachment", strconv.FormatUint(uint64(attachmentID), 10))
	}

	return attachment, nil
}

// articleOf answers whether the article is one of the ticket's; one that is not
// is not found, as it would be in a ticket the caller does not reach.
func (s *TicketApplicationService) articleOf(ctx context.Context, ticketID, articleID uint) error {
	articles, err := s.tickets.Articles(ctx, ticketID)
	if err != nil {
		return err
	}

	for _, article := range articles {
		if article.ID == articleID {
			return nil
		}
	}

	return apperror.NotFound("article", strconv.FormatUint(uint64(articleID), 10))
}
