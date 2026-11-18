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

	// sessions ends the sessions of a contact's account given a new password,
	// for the reason a reset of any account's ends them.
	sessions *SessionEnder
}

// NewCustomerHandler creates the customer handler.
func NewCustomerHandler(customers service.CustomerService, authz *Authorizer) *CustomerHandler {
	return &CustomerHandler{customers: customers, authz: authz}
}

// WithSessionEnder gives the handler a way to end the sessions of a contact's
// account given a new password.
func (h *CustomerHandler) WithSessionEnder(sessions *SessionEnder) *CustomerHandler {
	h.sessions = sessions

	return h
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

// ContactResponse is a contact as the API hands it out, naming its organisation
// as well as numbering it. HasPortalAccount says whether the contact signs in to
// the portal, without saying which account that is, which is the account's own
// business.
type ContactResponse struct {
	ID               uint      `json:"id"`
	OrganisationID   *uint     `json:"organisationId"`
	OrganisationName string    `json:"organisationName"`
	Name             string    `json:"name"`
	Email            string    `json:"email"`
	Phone            string    `json:"phone"`
	Note             string    `json:"note"`
	Valid            bool      `json:"valid"`
	HasPortalAccount bool      `json:"hasPortalAccount"`
	SeesOrganisation bool      `json:"seesOrganisation"`
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
	SeesOrganisation  *bool   `json:"seesOrganisation"`
}

// PortalAccessRequest gives a contact the portal: the initial password the
// account is made with, or given anew.
type PortalAccessRequest struct {
	Password string `json:"password"`
}

func newOrganisationResponse(o *model.Organisation) OrganisationResponse {
	return OrganisationResponse{
		ID: o.ID, Name: o.Name, Number: o.Number, Address: o.Address, URL: o.URL, Note: o.Note,
		Valid: o.Valid, CreatedAt: o.CreatedAt,
	}
}

func newContactResponse(c *model.Contact, organisations map[uint]string) ContactResponse {
	return ContactResponse{
		ID: c.ID, OrganisationID: c.OrganisationID, OrganisationName: nameIn(organisations, c.OrganisationID),
		Name: c.Name, Email: c.Email, Phone: c.Phone, Note: c.Note,
		Valid: c.Valid, HasPortalAccount: c.UserID != nil, SeesOrganisation: c.SeesOrganisation, CreatedAt: c.CreatedAt,
	}
}

// contactResponse answers with one contact, naming its organisation.
func (h *CustomerHandler) contactResponse(c *gofr.Context, contact *model.Contact) (any, error) {
	names, err := h.customers.OrganisationNames(c, []*model.Contact{contact})
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newContactResponse(contact, names), nil
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

	names, err := h.customers.OrganisationNames(c, contacts)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listOf(contacts, func(contact *model.Contact) ContactResponse { return newContactResponse(contact, names) })
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

	return h.contactResponse(c, contact)
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

	return h.contactResponse(c, contact)
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

	return h.contactResponse(c, contact)
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

// GivePortalAccess handles PUT /api/v1/contacts/{id}/portal-access: the contact
// is made an account to sign in to the portal with, under the password sent, or
// the account they have is given it - an initial password either way.
//
// customers:write, not users:write, because the account it makes is the
// contact's and can be nothing else: it holds the shipped customer role, and one
// that holds anything of the staff side is refused here.
func (h *CustomerHandler) GivePortalAccess(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req PortalAccessRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	contact, err := h.customers.GivePortalAccess(c, id, req.Password)
	if err != nil {
		return nil, toHTTPError(err)
	}

	// An account made a moment ago has none open; one given a new password may
	// have, on a device of whoever knew the old one.
	if contact.UserID != nil {
		h.sessions.afterReset(c, *contact.UserID)
	}

	return h.contactResponse(c, contact)
}

// TakePortalAccess handles DELETE /api/v1/contacts/{id}/portal-access: the
// account the contact signs in with is erased, and the tickets stay theirs.
func (h *CustomerHandler) TakePortalAccess(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCustomerWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.customers.TakePortalAccess(c, id); err != nil {
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
		ClearOrganisation: r.ClearOrganisation, Valid: r.Valid, SeesOrganisation: r.SeesOrganisation,
	}
}
