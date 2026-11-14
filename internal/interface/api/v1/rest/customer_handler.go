package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// CustomerHandler serves who tickets come from, under /organisations and
// /contacts. Reading takes customers:read and changing customers:write; neither
// is narrowed by queue.
type CustomerHandler struct {
	customers service.CustomerService
	authz     *Authorizer
}

// NewCustomerHandler creates the customer handler.
func NewCustomerHandler(customers service.CustomerService, authz *Authorizer) *CustomerHandler {
	return &CustomerHandler{customers: customers, authz: authz}
}

// OrganisationResponse is an organisation as the API hands it out.
type OrganisationResponse struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Number    string    `json:"number"`
	Address   string    `json:"address"`
	URL       string    `json:"url"`
	Note      string    `json:"note"`
	Valid     bool      `json:"valid"`
	CreatedAt time.Time `json:"createdAt"`
}

// OrganisationRequest creates or changes an organisation; on a change an absent
// field is left as it was.
type OrganisationRequest struct {
	Name    *string `json:"name"`
	Number  *string `json:"number"`
	Address *string `json:"address"`
	URL     *string `json:"url"`
	Note    *string `json:"note"`
	Valid   *bool   `json:"valid"`
}

// ContactResponse is a contact as the API hands it out. HasPortalAccount says
// whether the contact signs in to the portal, without saying which account that
// is, which is the account's own business.
type ContactResponse struct {
	ID               uint      `json:"id"`
	OrganisationID   *uint     `json:"organisationId"`
	Name             string    `json:"name"`
	Email            string    `json:"email"`
	Phone            string    `json:"phone"`
	Note             string    `json:"note"`
	Valid            bool      `json:"valid"`
	HasPortalAccount bool      `json:"hasPortalAccount"`
	CreatedAt        time.Time `json:"createdAt"`
}

// ContactRequest creates or changes a contact; on a change an absent field is
// left as it was, and ClearOrganisation takes the organisation off, because an
// id sent as null cannot be told apart from one that was not sent.
type ContactRequest struct {
	Name              *string `json:"name"`
	Email             *string `json:"email"`
	Phone             *string `json:"phone"`
	Note              *string `json:"note"`
	OrganisationID    *uint   `json:"organisationId"`
	ClearOrganisation bool    `json:"clearOrganisation"`
	Valid             *bool   `json:"valid"`
}

func newOrganisationResponse(o *model.Organisation) OrganisationResponse {
	return OrganisationResponse{
		ID: o.ID, Name: o.Name, Number: o.Number, Address: o.Address, URL: o.URL, Note: o.Note,
		Valid: o.Valid, CreatedAt: o.CreatedAt,
	}
}

func newContactResponse(c *model.Contact) ContactResponse {
	return ContactResponse{
		ID: c.ID, OrganisationID: c.OrganisationID, Name: c.Name, Email: c.Email, Phone: c.Phone, Note: c.Note,
		Valid: c.Valid, HasPortalAccount: c.UserID != nil, CreatedAt: c.CreatedAt,
	}
}

// ListOrganisations handles GET /api/v1/organisations.
func (h *CustomerHandler) ListOrganisations(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerRead); err != nil {
		return nil, err
	}

	filter, err := customerListFilter(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	organisations, total, err := h.customers.ListOrganisations(c, filter)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listOf(organisations, newOrganisationResponse)
	page.TotalCount = total

	return page, nil
}

// GetOrganisation handles GET /api/v1/organisations/{id}.
func (h *CustomerHandler) GetOrganisation(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerRead); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	organisation, err := h.customers.GetOrganisation(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newOrganisationResponse(organisation), nil
}

// CreateOrganisation handles POST /api/v1/organisations.
func (h *CustomerHandler) CreateOrganisation(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerWrite); err != nil {
		return nil, err
	}

	var req OrganisationRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	organisation, err := h.customers.CreateOrganisation(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newOrganisationResponse(organisation), nil
}

// UpdateOrganisation handles PUT /api/v1/organisations/{id}.
func (h *CustomerHandler) UpdateOrganisation(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req OrganisationRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	organisation, err := h.customers.UpdateOrganisation(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newOrganisationResponse(organisation), nil
}

// DeleteOrganisation handles DELETE /api/v1/organisations/{id}.
func (h *CustomerHandler) DeleteOrganisation(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.customers.DeleteOrganisation(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}

// ListContacts handles GET /api/v1/contacts.
func (h *CustomerHandler) ListContacts(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerRead); err != nil {
		return nil, err
	}

	filter, err := customerListFilter(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	contacts, total, err := h.customers.ListContacts(c, filter)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listOf(contacts, newContactResponse)
	page.TotalCount = total

	return page, nil
}

// GetContact handles GET /api/v1/contacts/{id}.
func (h *CustomerHandler) GetContact(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerRead); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	contact, err := h.customers.GetContact(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newContactResponse(contact), nil
}

// CreateContact handles POST /api/v1/contacts.
func (h *CustomerHandler) CreateContact(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerWrite); err != nil {
		return nil, err
	}

	var req ContactRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	contact, err := h.customers.CreateContact(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newContactResponse(contact), nil
}

// UpdateContact handles PUT /api/v1/contacts/{id}.
func (h *CustomerHandler) UpdateContact(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req ContactRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	contact, err := h.customers.UpdateContact(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newContactResponse(contact), nil
}

// DeleteContact handles DELETE /api/v1/contacts/{id}. ?confirm=true says that
// whoever asked knows the tickets for the contact will keep no contact; without
// it a contact tickets are for is refused, with how many.
func (h *CustomerHandler) DeleteContact(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.customers.DeleteContact(c, id, boolParam(c, "confirm")); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}

// customerListFilter reads what a listing of organisations or contacts asks for.
func customerListFilter(c *gofr.Context) (service.CustomerListFilter, error) {
	organisation, err := queryUint(c, "organisation")
	if err != nil {
		return service.CustomerListFilter{}, err
	}

	limit, err := strictInt(c.Param("limit"), "limit")
	if err != nil {
		return service.CustomerListFilter{}, err
	}

	page, err := strictInt(c.Param("page"), "page")
	if err != nil {
		return service.CustomerListFilter{}, err
	}

	return service.CustomerListFilter{
		Search: c.Param("q"), OrganisationID: organisation, ValidOnly: c.Param("valid") == "true",
		Limit: limit, Page: page,
	}, nil
}

func (r OrganisationRequest) command() command.OrganisationCommand {
	return command.OrganisationCommand{
		Name: r.Name, Number: r.Number, Address: r.Address, URL: r.URL, Note: r.Note, Valid: r.Valid,
	}
}

func (r ContactRequest) command() command.ContactCommand {
	return command.ContactCommand{
		Name: r.Name, Email: r.Email, Phone: r.Phone, Note: r.Note, OrganisationID: r.OrganisationID,
		ClearOrganisation: r.ClearOrganisation, Valid: r.Valid,
	}
}
