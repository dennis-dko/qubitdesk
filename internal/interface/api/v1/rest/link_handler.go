package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// LinkHandler serves the links between records. The rights it asks are the
// ends': reading a ticket's links takes tickets:read and an item's assets:read;
// making a link takes the right to change the record it starts from and to read
// the one it joins; undoing one takes the right to change the record it is
// undone through.
type LinkHandler struct {
	links *service.LinkApplicationService
	authz *Authorizer
}

// NewLinkHandler wires the handler.
func NewLinkHandler(links *service.LinkApplicationService, authz *Authorizer) *LinkHandler {
	return &LinkHandler{links: links, authz: authz}
}

// LinkRequest makes a link from one record to another.
type LinkRequest struct {
	SourceObject string `json:"sourceObject"`
	SourceID     uint   `json:"sourceId"`
	TargetObject string `json:"targetObject"`
	TargetID     uint   `json:"targetId"`
	Type         string `json:"type"`
}

// LinkedRecordResponse is a link as the record asked about sees it: the link's
// id and type, the record at its other end, and whether the record asked about
// is the source, which decides how a directed type reads.
type LinkedRecordResponse struct {
	ID        uint      `json:"id"`
	Type      string    `json:"type"`
	Outward   bool      `json:"outward"`
	Object    string    `json:"object"`
	ObjectID  uint      `json:"objectId"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
}

func newLinkedRecordResponse(record *service.LinkedRecord) LinkedRecordResponse {
	return LinkedRecordResponse{
		ID: record.Link.ID, Type: string(record.Link.Type), Outward: record.Outward, Object: string(record.Object),
		ObjectID: record.ID, Label: record.Label, CreatedAt: record.Link.CreatedAt,
	}
}

// rightFor is the right a kind of record takes to read, or to change.
//
// Which right a route asks for depends on a kind the request names, so every
// route here settles who is asking before it reads the request. Asked the
// other way round, somebody without a credential was told how to spell a kind
// of record where every other guarded route tells them to sign in.
func rightFor(object string, change bool) (string, error) {
	switch model.LinkObject(object) {
	case model.LinkTicket:
		if change {
			return model.PermTicketWrite, nil
		}

		return model.PermTicketRead, nil
	case model.LinkConfigItem:
		if change {
			return model.PermAssetWrite, nil
		}

		return model.PermAssetRead, nil
	case model.LinkChange:
		if change {
			return model.PermChangeWrite, nil
		}

		return model.PermChangeRead, nil
	}

	return "", apperror.InvalidFields("object")
}

// List handles GET /api/v1/links?object=&record=: the links of one record.
func (h *LinkHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.permittedPrincipal(c); err != nil {
		return nil, err
	}

	right, err := rightFor(c.Param("object"), false)
	if err != nil {
		return nil, toHTTPError(err)
	}

	caller, err := h.authz.Require(c, right)
	if err != nil {
		return nil, err
	}

	record, err := queryUint(c, "record")
	if err != nil {
		return nil, toHTTPError(err)
	}

	records, err := h.links.Of(c, caller, model.LinkObject(c.Param("object")), record)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(records, newLinkedRecordResponse), nil
}

// Create handles POST /api/v1/links.
func (h *LinkHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.permittedPrincipal(c); err != nil {
		return nil, err
	}

	var req LinkRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	change, err := rightFor(req.SourceObject, true)
	if err != nil {
		return nil, toHTTPError(apperror.InvalidFields("sourceObject"))
	}

	read, err := rightFor(req.TargetObject, false)
	if err != nil {
		return nil, toHTTPError(apperror.InvalidFields("targetObject"))
	}

	caller, err := h.authz.Require(c, change)
	if err != nil {
		return nil, err
	}

	if _, err := h.authz.Require(c, read); err != nil {
		return nil, err
	}

	record, err := h.links.Create(c, caller, service.LinkCommand{
		SourceObject: model.LinkObject(req.SourceObject), SourceID: req.SourceID,
		TargetObject: model.LinkObject(req.TargetObject), TargetID: req.TargetID, Type: model.LinkType(req.Type),
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newLinkedRecordResponse(record), nil
}

// Delete handles DELETE /api/v1/links/{id}?object=&record=: the link undone
// through the record named.
func (h *LinkHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.permittedPrincipal(c); err != nil {
		return nil, err
	}

	right, err := rightFor(c.Param("object"), true)
	if err != nil {
		return nil, toHTTPError(err)
	}

	caller, err := h.authz.Require(c, right)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	record, err := queryUint(c, "record")
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.links.Delete(c, caller, model.LinkObject(c.Param("object")), record, id); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}
