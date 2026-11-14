package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// OrganisationRepository stores the companies tickets come from.
type OrganisationRepository struct {
	base
}

// NewOrganisationRepository creates an organisation repository for the given
// dialect.
func NewOrganisationRepository(db DB, dialect string) *OrganisationRepository {
	return &OrganisationRepository{base{db: db, dialect: dialect}}
}

var _ repository.OrganisationRepository = (*OrganisationRepository)(nil)

const organisationColumns = "id, name, number, address, url, note, valid, created_at"

func (r *OrganisationRepository) List(
	ctx context.Context,
	filter repository.CustomerFilter,
) ([]*model.Organisation, uint, error) {
	where, args := customerConditions(filter, "(LOWER(name) LIKE ? ESCAPE '!' OR LOWER(number) LIKE ? ESCAPE '!')")

	var total uint

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM organisations WHERE "+where), args...).Scan(&total); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	args = append(args, filter.Limit, (max(filter.Page, 1)-1)*filter.Limit)

	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT "+organisationColumns+" FROM organisations WHERE "+where+" ORDER BY name, id LIMIT ? OFFSET ?"), args...)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	organisations := make([]*model.Organisation, 0)

	for rows.Next() {
		organisation, scanErr := scanOrganisation(rows)
		if scanErr != nil {
			return nil, 0, apperror.Internal(scanErr)
		}

		organisations = append(organisations, organisation)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	return organisations, total, nil
}

func (r *OrganisationRepository) GetByID(ctx context.Context, id uint) (*model.Organisation, error) {
	organisation, err := scanOrganisation(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+organisationColumns+" FROM organisations WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("organisation", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return organisation, nil
}

func (r *OrganisationRepository) Save(ctx context.Context, organisation *model.Organisation) (*model.Organisation, error) {
	id, err := r.insert(ctx,
		"INSERT INTO organisations (name, number, address, url, note, valid, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		organisation.Name, nullableText(organisation.Number), organisation.Address, organisation.URL,
		organisation.Note, organisation.Valid, organisation.CreatedAt)
	if err != nil {
		return nil, translateOrganisationErr(err, organisation.Number)
	}

	return r.GetByID(ctx, id)
}

func (r *OrganisationRepository) Update(ctx context.Context, organisation *model.Organisation) (*model.Organisation, error) {
	found, err := r.update(ctx, "organisations",
		"UPDATE organisations SET name = ?, number = ?, address = ?, url = ?, note = ?, valid = ? WHERE id = ?",
		organisation.ID,
		organisation.Name, nullableText(organisation.Number), organisation.Address, organisation.URL,
		organisation.Note, organisation.Valid, organisation.ID)
	if err != nil {
		return nil, translateOrganisationErr(err, organisation.Number)
	}

	if !found {
		return nil, apperror.NotFound("organisation", strconv.FormatUint(uint64(organisation.ID), 10))
	}

	return r.GetByID(ctx, organisation.ID)
}

func (r *OrganisationRepository) Delete(ctx context.Context, id uint) error {
	affected, err := r.exec(ctx, "DELETE FROM organisations WHERE id = ?", id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return apperror.NotFound("organisation", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

func (r *OrganisationRepository) CountUses(ctx context.Context, id uint) (int, int, error) {
	var contacts, tickets int

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM contacts WHERE organisation_id = ?"), id).Scan(&contacts); err != nil {
		return 0, 0, apperror.Internal(err)
	}

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM tickets WHERE organisation_id = ?"), id).Scan(&tickets); err != nil {
		return 0, 0, apperror.Internal(err)
	}

	return contacts, tickets, nil
}

// ContactRepository stores the people tickets come from.
type ContactRepository struct {
	base
}

// NewContactRepository creates a contact repository for the given dialect.
func NewContactRepository(db DB, dialect string) *ContactRepository {
	return &ContactRepository{base{db: db, dialect: dialect}}
}

var _ repository.ContactRepository = (*ContactRepository)(nil)

const contactColumns = "id, organisation_id, name, email, phone, note, valid, user_id, created_at"

func (r *ContactRepository) List(ctx context.Context, filter repository.CustomerFilter) ([]*model.Contact, uint, error) {
	where, args := customerConditions(filter, "(LOWER(name) LIKE ? ESCAPE '!' OR LOWER(email) LIKE ? ESCAPE '!')")

	if filter.OrganisationID != nil {
		where += " AND organisation_id = ?"
		args = append(args, *filter.OrganisationID)
	}

	var total uint

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM contacts WHERE "+where), args...).Scan(&total); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	args = append(args, filter.Limit, (max(filter.Page, 1)-1)*filter.Limit)

	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT "+contactColumns+" FROM contacts WHERE "+where+" ORDER BY name, id LIMIT ? OFFSET ?"), args...)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	contacts := make([]*model.Contact, 0)

	for rows.Next() {
		contact, scanErr := scanContact(rows)
		if scanErr != nil {
			return nil, 0, apperror.Internal(scanErr)
		}

		contacts = append(contacts, contact)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	return contacts, total, nil
}

func (r *ContactRepository) GetByID(ctx context.Context, id uint) (*model.Contact, error) {
	contact, err := scanContact(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+contactColumns+" FROM contacts WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("contact", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return contact, nil
}

func (r *ContactRepository) Save(ctx context.Context, contact *model.Contact) (*model.Contact, error) {
	id, err := r.insert(ctx,
		"INSERT INTO contacts (organisation_id, name, email, phone, note, valid, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		contact.OrganisationID, contact.Name, nullableText(contact.Email), contact.Phone, contact.Note,
		contact.Valid, contact.CreatedAt)
	if err != nil {
		return nil, translateContactErr(err, contact.Email)
	}

	return r.GetByID(ctx, id)
}

func (r *ContactRepository) Update(ctx context.Context, contact *model.Contact) (*model.Contact, error) {
	found, err := r.update(ctx, "contacts",
		"UPDATE contacts SET organisation_id = ?, name = ?, email = ?, phone = ?, note = ?, valid = ? WHERE id = ?",
		contact.ID,
		contact.OrganisationID, contact.Name, nullableText(contact.Email), contact.Phone, contact.Note,
		contact.Valid, contact.ID)
	if err != nil {
		return nil, translateContactErr(err, contact.Email)
	}

	if !found {
		return nil, apperror.NotFound("contact", strconv.FormatUint(uint64(contact.ID), 10))
	}

	return r.GetByID(ctx, contact.ID)
}

func (r *ContactRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := tx.exec(ctx, "UPDATE tickets SET contact_id = NULL WHERE contact_id = ?", id); err != nil {
			return apperror.Internal(err)
		}

		affected, err := tx.exec(ctx, "DELETE FROM contacts WHERE id = ?", id)
		if err != nil {
			return apperror.Internal(err)
		}

		if affected == 0 {
			return apperror.NotFound("contact", strconv.FormatUint(uint64(id), 10))
		}

		return nil
	})
}

func (r *ContactRepository) CountTickets(ctx context.Context, id uint) (int, error) {
	var tickets int

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM tickets WHERE contact_id = ?"), id).Scan(&tickets); err != nil {
		return 0, apperror.Internal(err)
	}

	return tickets, nil
}

// customerConditions is what a customer filter asks of the rows, whichever table:
// the search clause the caller names, given the words twice, and validity.
func customerConditions(filter repository.CustomerFilter, search string) (string, []any) {
	where := []string{"1 = 1"}

	var args []any

	if words := strings.TrimSpace(filter.Search); words != "" {
		pattern := "%" + strings.ToLower(likeEscaped(words)) + "%"
		where = append(where, search)
		args = append(args, pattern, pattern)
	}

	if filter.ValidOnly {
		where = append(where, "valid = ?")
		args = append(args, true)
	}

	return strings.Join(where, " AND "), args
}

func scanOrganisation(row scanner) (*model.Organisation, error) {
	var (
		organisation model.Organisation
		number       sql.NullString
		created      dateTime
	)

	err := row.Scan(&organisation.ID, &organisation.Name, &number, &organisation.Address, &organisation.URL,
		&organisation.Note, &organisation.Valid, &created)
	if err != nil {
		return nil, err
	}

	organisation.Number = number.String
	organisation.CreatedAt = created.Time

	return &organisation, nil
}

func scanContact(row scanner) (*model.Contact, error) {
	var (
		contact              model.Contact
		organisation, userID sql.NullInt64
		email                sql.NullString
		created              dateTime
	)

	err := row.Scan(&contact.ID, &organisation, &contact.Name, &email, &contact.Phone, &contact.Note,
		&contact.Valid, &userID, &created)
	if err != nil {
		return nil, err
	}

	contact.OrganisationID = optionalID(organisation)
	contact.Email = email.String
	contact.UserID = optionalID(userID)
	contact.CreatedAt = created.Time

	return &contact, nil
}

// nullableText stores an empty value as NULL, for the columns that are unique
// where they are given: every database counts two empty strings as the same
// value and two NULLs as different.
func nullableText(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func translateOrganisationErr(err error, number string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("an organisation numbered %q already exists", number).
			WithCode("organisationNumberTaken", number)
	}

	return apperror.Internal(err)
}

func translateContactErr(err error, email string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a contact with the address %q already exists", email).
			WithCode("contactEmailTaken", email)
	}

	return apperror.Internal(err)
}
