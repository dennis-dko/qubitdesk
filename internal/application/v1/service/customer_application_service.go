package service

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// CustomerService administers who tickets come from: the organisations and the
// contacts.
type CustomerService interface {
	ListOrganisations(ctx context.Context, filter CustomerListFilter) ([]*model.Organisation, uint, error)
	GetOrganisation(ctx context.Context, id uint) (*model.Organisation, error)
	CreateOrganisation(ctx context.Context, cmd command.OrganisationCommand) (*model.Organisation, error)
	UpdateOrganisation(ctx context.Context, id uint, cmd command.OrganisationCommand) (*model.Organisation, error)
	DeleteOrganisation(ctx context.Context, id uint) error

	ListContacts(ctx context.Context, filter CustomerListFilter) ([]*model.Contact, uint, error)
	OrganisationNames(ctx context.Context, contacts []*model.Contact) (map[uint]string, error)
	GetContact(ctx context.Context, id uint) (*model.Contact, error)
	CreateContact(ctx context.Context, cmd command.ContactCommand) (*model.Contact, error)
	UpdateContact(ctx context.Context, id uint, cmd command.ContactCommand) (*model.Contact, error)
	DeleteContact(ctx context.Context, id uint, confirmed bool) error

	// GivePortalAccess makes the contact an account to sign in to the portal
	// with, under the password given, or gives the account they have a new one;
	// either way it is an initial password, to be replaced at the first sign-in.
	GivePortalAccess(ctx context.Context, id uint, password string) (*model.Contact, error)

	// TakePortalAccess erases the account the contact signs in with.
	TakePortalAccess(ctx context.Context, id uint) error
}

// CustomerListFilter is what a listing of organisations or contacts asks for.
type CustomerListFilter struct {
	Search         string
	OrganisationID uint
	ValidOnly      bool
	Limit          int
	Page           int
}

// CustomerApplicationService is the CustomerService the application runs with.
//
// Customers are not narrowed by queue: a customer is the same customer whichever
// queue their ticket landed in, and an agent who could not look one up could not
// open a ticket for them.
//
// An organisation that contacts or tickets belong to is retired - made invalid -
// rather than deleted, as a ticket's state is. A contact is the person's data,
// and deleting one is allowed with its tickets kept and the contact taken off
// them, as an account goes and its tickets stay; when tickets are for it, the
// deletion has to be confirmed, and the refusal says how many.
type CustomerApplicationService struct {
	organisations repository.OrganisationRepository
	contacts      repository.ContactRepository
	now           func() time.Time

	// accounts are what a contact's portal account is made with and erased
	// with; nil keeps the contacts alone - see WithPortalAccounts.
	accounts *PortalAccounts
}

// NewCustomerApplicationService works on the two tables alone.
func NewCustomerApplicationService(
	organisations repository.OrganisationRepository,
	contacts repository.ContactRepository,
) *CustomerApplicationService {
	return &CustomerApplicationService{organisations: organisations, contacts: contacts, now: time.Now}
}

var _ CustomerService = (*CustomerApplicationService)(nil)

func (s *CustomerApplicationService) ListOrganisations(
	ctx context.Context,
	filter CustomerListFilter,
) ([]*model.Organisation, uint, error) {
	page, err := customerPage(filter)
	if err != nil {
		return nil, 0, err
	}

	return s.organisations.List(ctx, page)
}

func (s *CustomerApplicationService) GetOrganisation(ctx context.Context, id uint) (*model.Organisation, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.organisations.GetByID(ctx, id)
}

func (s *CustomerApplicationService) CreateOrganisation(
	ctx context.Context,
	cmd command.OrganisationCommand,
) (*model.Organisation, error) {
	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	organisation := &model.Organisation{Valid: true, CreatedAt: s.now().UTC()}
	if err := applyOrganisation(organisation, cmd); err != nil {
		return nil, err
	}

	return s.organisations.Save(ctx, organisation)
}

func (s *CustomerApplicationService) UpdateOrganisation(
	ctx context.Context,
	id uint,
	cmd command.OrganisationCommand,
) (*model.Organisation, error) {
	organisation, err := s.GetOrganisation(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := applyOrganisation(organisation, cmd); err != nil {
		return nil, err
	}

	return s.organisations.Update(ctx, organisation)
}

func (s *CustomerApplicationService) DeleteOrganisation(ctx context.Context, id uint) error {
	organisation, err := s.GetOrganisation(ctx, id)
	if err != nil {
		return err
	}

	contacts, tickets, err := s.organisations.CountUses(ctx, id)
	if err != nil {
		return err
	}

	if contacts > 0 || tickets > 0 {
		return apperror.Conflictf("%d contact(s) and %d ticket(s) belong to %q; make it invalid instead of deleting it",
			contacts, tickets, organisation.Name).WithCode("organisationInUse", organisation.Name, contacts, tickets)
	}

	return s.organisations.Delete(ctx, id)
}

func (s *CustomerApplicationService) ListContacts(
	ctx context.Context,
	filter CustomerListFilter,
) ([]*model.Contact, uint, error) {
	page, err := customerPage(filter)
	if err != nil {
		return nil, 0, err
	}

	return s.contacts.List(ctx, page)
}

func (s *CustomerApplicationService) GetContact(ctx context.Context, id uint) (*model.Contact, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.contacts.GetByID(ctx, id)
}

// OrganisationNames are the names of the organisations the contacts belong to,
// so a contact says where it works by itself: a screen that read the
// organisations to name them got two hundred at most, and named a contact of
// the next one as working nowhere. One that is gone has none.
func (s *CustomerApplicationService) OrganisationNames(
	ctx context.Context,
	contacts []*model.Contact,
) (map[uint]string, error) {
	names := map[uint]string{}

	for _, contact := range contacts {
		if contact.OrganisationID == nil {
			continue
		}

		if _, known := names[*contact.OrganisationID]; known {
			continue
		}

		organisation, err := s.organisations.GetByID(ctx, *contact.OrganisationID)
		if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
			return nil, err
		}

		names[*contact.OrganisationID] = ""
		if organisation != nil {
			names[*contact.OrganisationID] = organisation.Name
		}
	}

	return names, nil
}

func (s *CustomerApplicationService) CreateContact(ctx context.Context, cmd command.ContactCommand) (*model.Contact, error) {
	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	contact := &model.Contact{Valid: true, CreatedAt: s.now().UTC()}
	if err := s.applyContact(ctx, contact, cmd); err != nil {
		return nil, err
	}

	return s.contacts.Save(ctx, contact)
}

func (s *CustomerApplicationService) UpdateContact(
	ctx context.Context,
	id uint,
	cmd command.ContactCommand,
) (*model.Contact, error) {
	contact, err := s.GetContact(ctx, id)
	if err != nil {
		return nil, err
	}

	was := *contact

	if err := s.applyContact(ctx, contact, cmd); err != nil {
		return nil, err
	}

	account, err := s.accountInStep(ctx, &was, contact)
	if err != nil {
		return nil, err
	}

	updated, err := s.contacts.Update(ctx, contact)
	if err != nil {
		return nil, err
	}

	if account != nil {
		if _, err := s.accounts.Users.Update(ctx, account); err != nil {
			return nil, err
		}
	}

	return updated, nil
}

func (s *CustomerApplicationService) DeleteContact(ctx context.Context, id uint, confirmed bool) error {
	contact, err := s.GetContact(ctx, id)
	if err != nil {
		return err
	}

	tickets, err := s.contacts.CountTickets(ctx, id)
	if err != nil {
		return err
	}

	if tickets > 0 && !confirmed {
		return apperror.Conflictf("%d ticket(s) are for %q and would keep no contact; confirm to delete",
			tickets, contact.Name).WithCode("contactDeletionNeedsConfirming", contact.Name, tickets)
	}

	if err := s.eraseWithContact(ctx, contact); err != nil {
		return err
	}

	return s.contacts.Delete(ctx, id)
}

// applyOrganisation writes what cmd says onto organisation and refuses the result
// if any of it cannot be stored. The website is shown as a link, so only an http
// or https address is one: anything else - javascript:, data: - would be a link
// that runs what somebody typed.
func applyOrganisation(organisation *model.Organisation, cmd command.OrganisationCommand) error {
	for field, value := range map[*string]*string{
		&organisation.Name: cmd.Name, &organisation.Number: cmd.Number, &organisation.Address: cmd.Address,
		&organisation.URL: cmd.URL, &organisation.Note: cmd.Note,
	} {
		if value != nil {
			*field = strings.TrimSpace(*value)
		}
	}

	if cmd.Valid != nil {
		organisation.Valid = *cmd.Valid
	}

	var invalid []string

	if organisation.Name == "" || model.TooLong(organisation.Name, model.MaxNameLength) {
		invalid = append(invalid, "name")
	}

	if model.TooLong(organisation.Number, model.MaxOrganisationNumberLength) {
		invalid = append(invalid, "number")
	}

	if model.TooLong(organisation.Address, model.MaxAddressLength) {
		invalid = append(invalid, "address")
	}

	if model.TooLong(organisation.URL, model.MaxURLLength) || (organisation.URL != "" && !webAddress(organisation.URL)) {
		invalid = append(invalid, "url")
	}

	if model.TooLong(organisation.Note, model.MaxDescriptionLength) {
		invalid = append(invalid, "note")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return nil
}

// applyContact writes what cmd says onto contact and refuses the result if any of
// it cannot be stored. An organisation newly chosen has to exist and still be
// offered; one the contact already belongs to stays, retired or not.
func (s *CustomerApplicationService) applyContact(ctx context.Context, contact *model.Contact, cmd command.ContactCommand) error {
	for field, value := range map[*string]*string{
		&contact.Name: cmd.Name, &contact.Phone: cmd.Phone, &contact.Note: cmd.Note,
	} {
		if value != nil {
			*field = strings.TrimSpace(*value)
		}
	}

	if cmd.Email != nil {
		contact.Email = normalizeEmail(*cmd.Email)
	}

	if cmd.Valid != nil {
		contact.Valid = *cmd.Valid
	}

	if cmd.SeesOrganisation != nil {
		contact.SeesOrganisation = *cmd.SeesOrganisation
	}

	var invalid []string

	if contact.Name == "" || model.TooLong(contact.Name, model.MaxNameLength) {
		invalid = append(invalid, "name")
	}

	if contact.Email != "" && (!validEmail(contact.Email) || model.TooLong(contact.Email, model.MaxEmailLength)) {
		invalid = append(invalid, "email")
	}

	if model.TooLong(contact.Phone, model.MaxPhoneLength) {
		invalid = append(invalid, "phone")
	}

	if model.TooLong(contact.Note, model.MaxDescriptionLength) {
		invalid = append(invalid, "note")
	}

	if cmd.ClearOrganisation && cmd.OrganisationID != nil {
		invalid = append(invalid, "organisationId")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	switch {
	case cmd.ClearOrganisation:
		contact.OrganisationID = nil
	case cmd.OrganisationID != nil && (contact.OrganisationID == nil || *contact.OrganisationID != *cmd.OrganisationID):
		organisation, err := s.organisations.GetByID(ctx, *cmd.OrganisationID)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return apperror.InvalidFields("organisationId")
		}

		if err != nil {
			return err
		}

		if !organisation.Valid {
			return apperror.Conflictf("the organisation %q is no longer offered", organisation.Name).
				WithCode("organisationRetired", organisation.Name)
		}

		id := organisation.ID
		contact.OrganisationID = &id
	}

	return nil
}

// customerPage is a listing's page as the repositories take it, the same bounds
// as a page of tickets.
func customerPage(filter CustomerListFilter) (repository.CustomerFilter, error) {
	if filter.Limit == 0 {
		filter.Limit = DefaultTicketPage
	}

	if filter.Limit < 0 || filter.Limit > MaxTicketPage {
		return repository.CustomerFilter{}, apperror.InvalidFields("limit")
	}

	if filter.Page < 0 {
		return repository.CustomerFilter{}, apperror.InvalidFields("page")
	}

	page := repository.CustomerFilter{
		Search: filter.Search, ValidOnly: filter.ValidOnly, Limit: filter.Limit, Page: filter.Page,
	}

	if filter.OrganisationID != 0 {
		id := filter.OrganisationID
		page.OrganisationID = &id
	}

	return page, nil
}

// webAddress reports whether a website is an http or https address, the only
// two a link may safely point at.
func webAddress(raw string) bool {
	parsed, err := url.Parse(raw)

	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}
