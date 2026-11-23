package rest

import (
	"net/url"
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// TicketSearchHandler serves the searches an agent saved, under /me because
// each is the caller's own.
type TicketSearchHandler struct {
	searches *service.TicketSearchApplicationService
	authz    *Authorizer
}

// NewTicketSearchHandler creates the saved searches' handler.
func NewTicketSearchHandler(searches *service.TicketSearchApplicationService, authz *Authorizer) *TicketSearchHandler {
	return &TicketSearchHandler{searches: searches, authz: authz}
}

// TicketSearchResponse is a saved search as the API hands it out.
type TicketSearchResponse struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Query     string    `json:"query"`
	CreatedAt time.Time `json:"createdAt"`
}

// SaveTicketSearchRequest is a search to keep: a name, and the listing's query.
type SaveTicketSearchRequest struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

// Mine handles GET /api/v1/me/ticket-searches.
func (h *TicketSearchHandler) Mine(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	searches, err := h.searches.Mine(c, caller)
	if err != nil {
		return nil, toHTTPError(err)
	}

	items := make([]TicketSearchResponse, 0, len(searches))
	for _, search := range searches {
		items = append(items, newTicketSearchResponse(search))
	}

	return listResponse[TicketSearchResponse]{Items: items, TotalCount: uint(len(items))}, nil
}

// Save handles POST /api/v1/me/ticket-searches.
func (h *TicketSearchHandler) Save(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	var request SaveTicketSearchRequest
	if err := c.Bind(&request); err != nil {
		return nil, toHTTPError(apperror.Invalidf("invalid request body"))
	}

	if err := checkedSearchQuery(request.Query, caller); err != nil {
		return nil, toHTTPError(err)
	}

	search, err := h.searches.Save(c, caller, request.Name, request.Query)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketSearchResponse(search), nil
}

// Delete handles DELETE /api/v1/me/ticket-searches/{id}.
func (h *TicketSearchHandler) Delete(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.searches.Delete(c, caller, id); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}

// checkedSearchQuery reads a search's query the way the listing reads its own,
// refusing what the listing would refuse and a parameter the listing never
// asks for - which it would otherwise answer without, so that the search ran
// as something nobody saved. A page and its size are where somebody was, not
// what they searched for.
func checkedSearchQuery(query string, caller *service.Principal) error {
	values, err := url.ParseQuery(query)
	if err != nil {
		return apperror.InvalidFields("query")
	}

	asked := map[string]bool{}

	if _, err := listingFilter(func(name string) string {
		asked[name] = true

		return values.Get(name)
	}, caller); err != nil {
		return err
	}

	for key := range values {
		if !asked[key] || key == "limit" || key == "page" {
			return apperror.InvalidFields("query")
		}
	}

	return nil
}

func newTicketSearchResponse(search *model.TicketSearch) TicketSearchResponse {
	return TicketSearchResponse{ID: search.ID, Name: search.Name, Query: search.Query, CreatedAt: search.CreatedAt}
}
