package memory

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// OrganisationRepository keeps organisations in memory, with the database's rule
// that a number is unique where it is given. How many contacts and tickets
// belong to one is whatever a test says with UseIn, for the reason the
// catalog's counts are.
type OrganisationRepository struct {
	items *store[model.Organisation]

	mu   sync.Mutex
	uses map[uint][2]int
}

// NewOrganisationRepository creates an empty organisation repository.
func NewOrganisationRepository() *OrganisationRepository {
	return &OrganisationRepository{items: newStore[model.Organisation]("organisation"), uses: map[uint][2]int{}}
}

var _ repository.OrganisationRepository = (*OrganisationRepository)(nil)

// UseIn makes the organisation with this id look as if contacts and tickets
// belonged to it.
func (r *OrganisationRepository) UseIn(id uint, contacts, tickets int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.uses[id] = [2]int{contacts, tickets}
}

func (r *OrganisationRepository) List(
	_ context.Context,
	filter repository.CustomerFilter,
) ([]*model.Organisation, uint, error) {
	words := strings.ToLower(strings.TrimSpace(filter.Search))

	matching := slices.DeleteFunc(r.items.all(), func(o *model.Organisation) bool {
		return (filter.ValidOnly && !o.Valid) ||
			(words != "" && !strings.Contains(strings.ToLower(o.Name), words) &&
				!strings.Contains(strings.ToLower(o.Number), words))
	})

	slices.SortFunc(matching, func(a, b *model.Organisation) int { return strings.Compare(a.Name, b.Name) })

	return page(matching, filter), uint(len(matching)), nil
}

func (r *OrganisationRepository) GetByID(_ context.Context, id uint) (*model.Organisation, error) {
	return r.items.get(id)
}

func (r *OrganisationRepository) Save(_ context.Context, organisation *model.Organisation) (*model.Organisation, error) {
	if r.numberTaken(organisation) {
		return nil, organisationNumberTaken(organisation.Number)
	}

	return r.items.add(organisation, func(o *model.Organisation, id uint) { o.ID = id }), nil
}

func (r *OrganisationRepository) Update(_ context.Context, organisation *model.Organisation) (*model.Organisation, error) {
	if r.numberTaken(organisation) {
		return nil, organisationNumberTaken(organisation.Number)
	}

	return r.items.update(organisation.ID, organisation)
}

func (r *OrganisationRepository) Delete(_ context.Context, id uint) error {
	return r.items.delete(id)
}

func (r *OrganisationRepository) CountUses(_ context.Context, id uint) (int, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.uses[id][0], r.uses[id][1], nil
}

func (r *OrganisationRepository) numberTaken(organisation *model.Organisation) bool {
	if organisation.Number == "" {
		return false
	}

	return slices.ContainsFunc(r.items.all(), func(other *model.Organisation) bool {
		return other.ID != organisation.ID && other.Number == organisation.Number
	})
}

func organisationNumberTaken(number string) error {
	return apperror.Conflictf("an organisation numbered %q already exists", number).
		WithCode("organisationNumberTaken", number)
}

// ContactRepository keeps contacts in memory, with the database's rule that an
// address is unique where it is given. How many tickets are for one is whatever
// a test says with UseInTickets.
type ContactRepository struct {
	items *store[model.Contact]

	mu      sync.Mutex
	tickets map[uint]int

	// users answers which role a linked account holds, for
	// CountLinkedWithRole; without it none holds any - see KnowAccounts.
	users *UserRepository
}

// NewContactRepository creates an empty contact repository.
func NewContactRepository() *ContactRepository {
	return &ContactRepository{items: newStore[model.Contact]("contact"), tickets: map[uint]int{}}
}

var _ repository.ContactRepository = (*ContactRepository)(nil)

// KnowAccounts lets the repository say which roles the accounts contacts sign
// in with hold, as the database answers it by joining the two tables.
func (r *ContactRepository) KnowAccounts(users *UserRepository) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.users = users
}

// ForgetAccount takes the account off the contact that signs in with it, which
// is what the erasure of an account does to the contacts table.
func (r *ContactRepository) ForgetAccount(userID uint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, contact := range r.items.all() {
		if contact.UserID != nil && *contact.UserID == userID {
			contact.UserID = nil
			_, _ = r.items.update(contact.ID, contact)
		}
	}
}

// UseInTickets makes the contact with this id look as if count tickets were for it.
func (r *ContactRepository) UseInTickets(id uint, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tickets[id] = count
}

func (r *ContactRepository) List(_ context.Context, filter repository.CustomerFilter) ([]*model.Contact, uint, error) {
	words := strings.ToLower(strings.TrimSpace(filter.Search))

	matching := slices.DeleteFunc(r.items.all(), func(c *model.Contact) bool {
		return (filter.ValidOnly && !c.Valid) ||
			(filter.OrganisationID != nil && (c.OrganisationID == nil || *c.OrganisationID != *filter.OrganisationID)) ||
			(words != "" && !strings.Contains(strings.ToLower(c.Name), words) &&
				!strings.Contains(strings.ToLower(c.Email), words))
	})

	slices.SortFunc(matching, func(a, b *model.Contact) int { return strings.Compare(a.Name, b.Name) })

	return page(matching, filter), uint(len(matching)), nil
}

func (r *ContactRepository) GetByID(_ context.Context, id uint) (*model.Contact, error) {
	return r.items.get(id)
}

func (r *ContactRepository) GetByUserID(_ context.Context, userID uint) (*model.Contact, error) {
	for _, contact := range r.items.all() {
		if contact.UserID != nil && *contact.UserID == userID {
			return contact, nil
		}
	}

	return nil, apperror.NotFound("contact", strconv.FormatUint(uint64(userID), 10))
}

func (r *ContactRepository) Save(_ context.Context, contact *model.Contact) (*model.Contact, error) {
	if r.emailTaken(contact) {
		return nil, contactEmailTaken(contact.Email)
	}

	saved := *contact
	saved.UserID = nil

	return r.items.add(&saved, func(c *model.Contact, id uint) { c.ID = id }), nil
}

// Update leaves the account where it was, as the database's statement does.
func (r *ContactRepository) Update(_ context.Context, contact *model.Contact) (*model.Contact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.emailTaken(contact) {
		return nil, contactEmailTaken(contact.Email)
	}

	stored, err := r.items.get(contact.ID)
	if err != nil {
		return nil, err
	}

	updated := *contact
	updated.UserID = stored.UserID

	return r.items.update(contact.ID, &updated)
}

func (r *ContactRepository) LinkAccount(_ context.Context, contactID, userID uint) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// An update matching no row is not an error, as in the database.
	contact, err := r.items.get(contactID)
	if apperror.KindOf(err) == apperror.KindNotFound || (err == nil && contact.UserID != nil) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	contact.UserID = &userID
	_, err = r.items.update(contactID, contact)

	return err == nil, err
}

func (r *ContactRepository) CountLinkedWithRole(ctx context.Context, roleID uint) (int, error) {
	r.mu.Lock()
	users := r.users
	r.mu.Unlock()

	count := 0

	for _, contact := range r.items.all() {
		if contact.UserID == nil || users == nil {
			continue
		}

		if user, err := users.GetByID(ctx, *contact.UserID); err == nil && user.RoleID == roleID {
			count++
		}
	}

	return count, nil
}

func (r *ContactRepository) Delete(_ context.Context, id uint) error {
	if err := r.items.delete(id); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.tickets, id)

	return nil
}

func (r *ContactRepository) CountTickets(_ context.Context, id uint) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.tickets[id], nil
}

func (r *ContactRepository) emailTaken(contact *model.Contact) bool {
	if contact.Email == "" {
		return false
	}

	return slices.ContainsFunc(r.items.all(), func(other *model.Contact) bool {
		return other.ID != contact.ID && other.Email == contact.Email
	})
}

func contactEmailTaken(email string) error {
	return apperror.Conflictf("a contact with the address %q already exists", email).
		WithCode("contactEmailTaken", email)
}

// page is one page of a sorted listing.
func page[T any](items []*T, filter repository.CustomerFilter) []*T {
	start := (max(filter.Page, 1) - 1) * filter.Limit
	if filter.Limit <= 0 || start >= len(items) {
		return []*T{}
	}

	return items[start:min(start+filter.Limit, len(items))]
}
