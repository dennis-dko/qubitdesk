package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// AdminLogService keeps and reads the administration log: who changed how the
// installation is run, and when.
//
// It keeps ids and reads names. A name kept in an entry would outlive the person
// it names once their account is erased, which is the one thing an erasure must
// not leave behind; an id names nobody once its account is gone, because no id is
// handed out twice. A role's name is not a person's and is read the same way, so
// a role renamed since appears under its new name; a contact given the portal or
// having it taken away is named the way an account is.
type AdminLogService struct {
	events   repository.AdminEventRepository
	users    repository.UserRepository
	roles    repository.RoleRepository
	contacts repository.ContactRepository
	catalog  repository.GeneralCatalogRepository
	now      func() time.Time
}

// NewAdminLogService needs the accounts, the roles and the contacts, to name
// them.
func NewAdminLogService(events repository.AdminEventRepository, users repository.UserRepository,
	roles repository.RoleRepository, contacts repository.ContactRepository) *AdminLogService {
	return &AdminLogService{events: events, users: users, roles: roles, contacts: contacts, now: time.Now}
}

// WithCatalog names the classes of configuration items a definition was written
// for, from the general catalog they are kept in.
func (s *AdminLogService) WithCatalog(catalog repository.GeneralCatalogRepository) *AdminLogService {
	s.catalog = catalog

	return s
}

// AdminLogEntry is a change as it is shown: its ids, and the names they carry now,
// empty for an account or a role that is gone and for a change with no target.
type AdminLogEntry struct {
	model.AdminEvent

	ActorName  string
	TargetName string
}

// Record keeps one change made by the account actorID.
func (s *AdminLogService) Record(ctx context.Context, actorID uint, action string, targetID uint, detail string) error {
	return s.events.Record(ctx, &model.AdminEvent{
		At: s.now().UTC(), ActorID: actorID, Action: action, TargetID: targetID, Detail: detail,
	})
}

// Page reads up to limit changes older than beforeID - zero for the newest - with
// the names their ids carry now, and says whether there are older ones still.
func (s *AdminLogService) Page(ctx context.Context, beforeID uint, limit int) ([]AdminLogEntry, bool, error) {
	events, err := s.events.Before(ctx, beforeID, limit+1)
	if err != nil {
		return nil, false, err
	}

	more := len(events) > limit
	if more {
		events = events[:limit]
	}

	names := namer{users: s.users, roles: s.roles, contacts: s.contacts, catalog: s.catalog, known: map[string]string{}}
	entries := make([]AdminLogEntry, 0, len(events))

	for _, event := range events {
		entry := AdminLogEntry{AdminEvent: event}

		if entry.ActorName, err = names.user(ctx, event.ActorID); err != nil {
			return nil, false, err
		}

		switch {
		case event.TargetID == 0:
		case strings.HasPrefix(event.Action, "user."):
			entry.TargetName, err = names.user(ctx, event.TargetID)
		case strings.HasPrefix(event.Action, "role."):
			entry.TargetName, err = names.role(ctx, event.TargetID)
		case strings.HasPrefix(event.Action, "contact."):
			entry.TargetName, err = names.contact(ctx, event.TargetID)
		case strings.HasPrefix(event.Action, "classDefinition."):
			entry.TargetName, err = names.catalogItem(ctx, event.TargetID)
		}

		if err != nil {
			return nil, false, err
		}

		entries = append(entries, entry)
	}

	return entries, more, nil
}

// namer reads the names a page of the log needs, each once.
type namer struct {
	users    repository.UserRepository
	roles    repository.RoleRepository
	contacts repository.ContactRepository
	catalog  repository.GeneralCatalogRepository
	known    map[string]string
}

// catalogItem names a value of the general catalog - the class a definition
// was written for - and nothing where the log was given no catalog to ask.
func (n namer) catalogItem(ctx context.Context, id uint) (string, error) {
	if n.catalog == nil {
		return "", nil
	}

	return n.read("catalogitem", id, func() (string, error) {
		item, err := n.catalog.GetByID(ctx, id)
		if err != nil {
			return "", err
		}

		return item.Name, nil
	})
}

func (n namer) user(ctx context.Context, id uint) (string, error) {
	return n.read("user", id, func() (string, error) {
		user, err := n.users.GetByID(ctx, id)
		if err != nil {
			return "", err
		}

		return user.Name, nil
	})
}

func (n namer) role(ctx context.Context, id uint) (string, error) {
	return n.read("role", id, func() (string, error) {
		role, err := n.roles.GetByID(ctx, id)
		if err != nil {
			return "", err
		}

		return role.Name, nil
	})
}

func (n namer) contact(ctx context.Context, id uint) (string, error) {
	return n.read("contact", id, func() (string, error) {
		contact, err := n.contacts.GetByID(ctx, id)
		if err != nil {
			return "", err
		}

		return contact.Name, nil
	})
}

// read answers from what this page has already read, and an account, a role or
// a contact that is gone with no name rather than an error: being gone is what
// the log says about it.
func (n namer) read(kind string, id uint, lookUp func() (string, error)) (string, error) {
	key := kind + ":" + strconv.FormatUint(uint64(id), 10)
	if name, found := n.known[key]; found {
		return name, nil
	}

	name, err := lookUp()
	if err != nil {
		if detail, ok := apperror.Detail(err); !ok || detail.Kind != apperror.KindNotFound {
			return "", err
		}

		name = ""
	}

	n.known[key] = name

	return name, nil
}
