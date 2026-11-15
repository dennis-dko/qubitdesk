package rest

import (
	"io"
	"mime/multipart"

	"gofr.dev/pkg/gofr"
	"gofr.dev/pkg/gofr/http/response"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// AttachmentResponse is a file of an article, without its content.
type AttachmentResponse struct {
	ID          uint   `json:"id"`
	ArticleID   uint   `json:"articleId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
}

// AttachmentRequest is the upload of one file.
type AttachmentRequest struct {
	File multipart.FileHeader `file:"file"`
}

func newAttachmentResponse(attachment *model.Attachment) AttachmentResponse {
	return AttachmentResponse{
		ID: attachment.ID, ArticleID: attachment.ArticleID, Filename: attachment.Filename,
		ContentType: attachment.ContentType, Size: attachment.Size,
	}
}

// UploadAttachment handles POST /api/v1/tickets/{id}/articles/{article}/attachments.
func (h *TicketHandler) UploadAttachment(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	articleID, err := parseUint(c.PathParam("article"), "article")
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req AttachmentRequest
	if err := c.Bind(&req); err != nil {
		return nil, toHTTPError(apperror.Invalidf("the upload could not be read: %v", err).
			WithCode("uploadUnreadable"))
	}

	file, err := req.File.Open()
	if err != nil {
		return nil, toHTTPError(apperror.Invalidf("the upload could not be opened: %v", err).
			WithCode("uploadUnreadable"))
	}
	defer func() { _ = file.Close() }()

	// One byte past the bound is enough to know it is too large.
	content, err := io.ReadAll(io.LimitReader(file, model.MaxAttachmentBytes+1))
	if err != nil {
		return nil, toHTTPError(apperror.Invalidf("the upload could not be read: %v", err).
			WithCode("uploadUnreadable"))
	}

	attachment, err := h.tickets.AddAttachment(c, caller, id, articleID, service.AttachedFile{
		Name: req.File.Filename, Type: req.File.Header.Get("Content-Type"), Content: content,
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newAttachmentResponse(attachment), nil
}

// DownloadAttachment handles GET /api/v1/tickets/{id}/attachments/{attachment}.
//
// Always as bytes, whatever type the file was sent under. GoFr keeps the headers
// to itself, so no Content-Disposition can say "save this", and a page or a
// script served from this origin under its own type would run as this
// application - script-src 'self' would let in a script somebody uploaded. The
// page names the file and its type when it saves it.
func (h *TicketHandler) DownloadAttachment(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	attachmentID, err := parseUint(c.PathParam("attachment"), "attachment")
	if err != nil {
		return nil, toHTTPError(err)
	}

	attachment, err := h.tickets.Attachment(c, caller, id, attachmentID)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return response.File{Content: attachment.Content, ContentType: "application/octet-stream"}, nil
}

// attachmentsByArticle lists a ticket's files under the article each came with.
func attachmentsByArticle(attachments []*model.Attachment) map[uint][]AttachmentResponse {
	byArticle := map[uint][]AttachmentResponse{}

	for _, attachment := range attachments {
		byArticle[attachment.ArticleID] = append(byArticle[attachment.ArticleID], newAttachmentResponse(attachment))
	}

	return byArticle
}
