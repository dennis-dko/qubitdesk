package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// ConfigItemApplicationService keeps the configuration items, as KIX18 keeps
// its assets: an item of a class, in versions each written under the
// definition its class has when it is written. A version is never changed; a
// change to an item is its next version, so what it said before stays readable
// the way it was written.
//
// What a version holds is checked against the definition before it is kept:
// every attribute as often as the definition allows, every value of the type
// its attribute is, and what a list, a reference, a contact or an organisation
// names there to name. A retired state, list value, contact or organisation is
// not newly chosen, and stays where the version the item stands at already
// names it - the rule a ticket keeps for its state.
type ConfigItemApplicationService struct {
	items         repository.ConfigItemRepository
	definitions   repository.ClassDefinitionRepository
	catalog       repository.GeneralCatalogRepository
	contacts      repository.ContactRepository
	organisations repository.OrganisationRepository
	users         repository.UserRepository
	now           func() time.Time
}

// ConfigItemSources are what an item's values may name besides other items,
// and the accounts whose names say who wrote a version.
type ConfigItemSources struct {
	Definitions   repository.ClassDefinitionRepository
	Catalog       repository.GeneralCatalogRepository
	Contacts      repository.ContactRepository
	Organisations repository.OrganisationRepository
	Users         repository.UserRepository
}

// NewConfigItemApplicationService keeps the items and checks what they hold.
func NewConfigItemApplicationService(
	items repository.ConfigItemRepository,
	sources ConfigItemSources,
) *ConfigItemApplicationService {
	return &ConfigItemApplicationService{
		items: items, definitions: sources.Definitions, catalog: sources.Catalog, contacts: sources.Contacts,
		organisations: sources.Organisations, users: sources.Users, now: time.Now,
	}
}

// ConfigItemListFilter is what a listing of items asks for.
type ConfigItemListFilter struct {
	ClassIDs []uint
	Search   string
	Limit    int
	Page     int
}

// ConfigItemVersionCommand is one version of an item as somebody writes it.
type ConfigItemVersionCommand struct {
	Name              string
	DeploymentStateID uint
	IncidentStateID   uint
	Data              model.AttributeData
}

// ConfigItemWithVersion is an item and the version it stands at.
type ConfigItemWithVersion struct {
	Item    *model.ConfigItem
	Version *model.ConfigItemVersion
}

// List is one page of the items the filter allows, by name, and how many it
// allows altogether.
func (s *ConfigItemApplicationService) List(
	ctx context.Context,
	filter ConfigItemListFilter,
) ([]*model.ConfigItem, uint, error) {
	if filter.Limit == 0 {
		filter.Limit = DefaultTicketPage
	}

	if filter.Limit < 0 || filter.Limit > MaxTicketPage {
		return nil, 0, apperror.InvalidFields("limit")
	}

	if filter.Page < 0 {
		return nil, 0, apperror.InvalidFields("page")
	}

	return s.items.List(ctx, repository.ConfigItemFilter{
		ClassIDs: filter.ClassIDs, Search: filter.Search, Limit: filter.Limit, Page: filter.Page,
	})
}

// Names are the accounts' names by id, for saying who wrote a version and who
// changed an item last. An account erased since is in none of them, and is
// shown by its number: what it wrote stays, as an article does.
func (s *ConfigItemApplicationService) Names(ctx context.Context) (map[uint]string, error) {
	users, err := s.users.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	names := make(map[uint]string, len(users))
	for _, user := range users {
		names[user.ID] = user.Name
	}

	return names, nil
}

// Get is an item and the version it stands at.
func (s *ConfigItemApplicationService) Get(ctx context.Context, id uint) (*ConfigItemWithVersion, error) {
	item, err := s.items.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	version, err := s.items.Version(ctx, item.VersionID)
	if err != nil {
		return nil, err
	}

	return &ConfigItemWithVersion{Item: item, Version: version}, nil
}

// Versions is every version of an item, the newest first.
func (s *ConfigItemApplicationService) Versions(ctx context.Context, id uint) ([]*model.ConfigItemVersion, error) {
	if _, err := s.items.GetByID(ctx, id); err != nil {
		return nil, err
	}

	return s.items.Versions(ctx, id)
}

// Create makes an item of a class still offered, its first version written
// under the definition the class has now, by the account given.
func (s *ConfigItemApplicationService) Create(
	ctx context.Context,
	author uint,
	classID uint,
	cmd ConfigItemVersionCommand,
) (*ConfigItemWithVersion, error) {
	class, err := s.entry(ctx, classID, model.CatalogCIClass)
	if err != nil {
		return nil, err
	}

	if class == nil {
		return nil, apperror.InvalidFields("classId")
	}

	if !class.Valid {
		return nil, apperror.Conflictf("the class %q is retired", class.Name).
			WithCode("configItemClassRetired", class.Name)
	}

	version, err := s.version(ctx, class, cmd, &model.ConfigItemVersion{})
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	version.CreatedAt, version.CreatedBy = now, author

	item, err := s.items.Create(ctx, &model.ConfigItem{ClassID: class.ID, CreatedAt: now, CreatedBy: author}, version,
		func(id uint) string { return fmt.Sprintf("%08d", id) })
	if err != nil {
		return nil, err
	}

	return s.Get(ctx, item.ID)
}

// AddVersion writes an item's next version, under the definition its class has
// now, by the account given. It is refused when it says what the version it
// stands at says, under the same definition. An item of a retired class goes on
// taking versions: retiring a class stops new items, not the ones there are.
func (s *ConfigItemApplicationService) AddVersion(
	ctx context.Context,
	author uint,
	id uint,
	cmd ConfigItemVersionCommand,
) (*ConfigItemWithVersion, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	class, err := s.catalog.GetByID(ctx, current.Item.ClassID)
	if err != nil {
		return nil, err
	}

	version, err := s.version(ctx, class, cmd, current.Version)
	if err != nil {
		return nil, err
	}

	if sameVersion(current.Version, version) {
		return nil, apperror.Invalidf("the version says what the item says now").WithCode("configItemUnchanged")
	}

	version.ItemID, version.CreatedAt, version.CreatedBy = id, s.now().UTC(), author

	if _, err := s.items.AddVersion(ctx, version); err != nil {
		return nil, err
	}

	return s.Get(ctx, id)
}

// Delete removes an item with every version of it.
func (s *ConfigItemApplicationService) Delete(ctx context.Context, id uint) error {
	return s.items.Delete(ctx, id)
}

// version checks what a version says against its class's definition as it
// stands now, and answers it ready to be written. stood is the version the item
// stands at, empty for an item still to be made: what it names may stay named
// although it has been retired since.
func (s *ConfigItemApplicationService) version(
	ctx context.Context,
	class *model.CatalogItem,
	cmd ConfigItemVersionCommand,
	stood *model.ConfigItemVersion,
) (*model.ConfigItemVersion, error) {
	definition, err := s.definitions.Current(ctx, class.ID)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil, apperror.Invalidf("the class %s has no definition", class.Name).
			WithCode("configItemNoDefinition", class.Name)
	}

	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(cmd.Name)

	var invalid []string

	if name == "" || model.TooLong(name, model.MaxConfigItemNameLength) {
		invalid = append(invalid, "name")
	}

	deployment, err := s.entry(ctx, cmd.DeploymentStateID, model.CatalogDeploymentState)
	if err != nil {
		return nil, err
	}

	if deployment == nil {
		invalid = append(invalid, "deploymentStateId")
	}

	incident, err := s.entry(ctx, cmd.IncidentStateID, model.CatalogIncidentState)
	if err != nil {
		return nil, err
	}

	if incident == nil {
		invalid = append(invalid, "incidentStateId")
	}

	if len(invalid) > 0 {
		return nil, apperror.InvalidFields(invalid...)
	}

	if err := stillOffered(deployment, stood.DeploymentStateID == deployment.ID); err != nil {
		return nil, err
	}

	if err := stillOffered(incident, stood.IncidentStateID == incident.ID); err != nil {
		return nil, err
	}

	data := cmd.Data
	if data == nil {
		data = model.AttributeData{}
	}

	if err := s.checkData(ctx, definition.Attributes, data, valuePath{}, heldValues(stood.Data)); err != nil {
		return nil, err
	}

	return &model.ConfigItemVersion{
		DefinitionID: definition.ID, Name: name, DeploymentStateID: cmd.DeploymentStateID,
		IncidentStateID: cmd.IncidentStateID, Data: data,
	}, nil
}

// entry is the value of the general catalog's class an id names, or nil where
// it names no value of that class. An error is one of reading, never of the id.
func (s *ConfigItemApplicationService) entry(ctx context.Context, id uint, class string) (*model.CatalogItem, error) {
	if id == 0 {
		return nil, nil
	}

	entry, err := s.catalog.GetByID(ctx, id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if entry.Class != class {
		return nil, nil
	}

	return entry, nil
}

// stillOffered refuses a value of the general catalog that has been retired,
// unless the version the item stands at already names it in the same place.
func stillOffered(entry *model.CatalogItem, kept bool) error {
	if entry.Valid || kept {
		return nil
	}

	return apperror.Conflictf("the value %q is retired", entry.Name).WithCode("catalogValueRetired", entry.Name)
}

// valuePath names where a value stands two ways: as a refusal shows it, with
// the place of an occurrence among several, and by its keys alone - which is
// how a value the item already holds is recognised wherever it has moved to
// among the occurrences of its attribute.
type valuePath struct {
	shown, keys string
}

func (p valuePath) to(key string) valuePath {
	return valuePath{shown: joinPath(p.shown, key), keys: joinPath(p.keys, key)}
}

func (p valuePath) at(i int) valuePath {
	return valuePath{shown: p.shown + "[" + strconv.Itoa(i+1) + "]", keys: p.keys}
}

// heldValues is every value a version holds, each under the keys of where it
// stands. A key is letters and digits only, so neither separator can be part of
// one.
func heldValues(data model.AttributeData) map[string]bool {
	held := map[string]bool{}

	var walk func(data model.AttributeData, parent string)

	walk = func(data model.AttributeData, parent string) {
		for key, occurrences := range data {
			path := joinPath(parent, key)

			for _, occurrence := range occurrences {
				held[path+"="+occurrence.Value] = true
				walk(occurrence.Sub, path)
			}
		}
	}

	walk(data, "")

	return held
}

// checkData checks one level of a version's values against the attributes of
// the same level of the definition.
func (s *ConfigItemApplicationService) checkData(
	ctx context.Context,
	attributes []model.ClassAttribute,
	data model.AttributeData,
	parent valuePath,
	held map[string]bool,
) error {
	for key := range data {
		if !slices.ContainsFunc(attributes, func(a model.ClassAttribute) bool { return a.Key == key }) {
			path := parent.to(key).shown

			return apperror.Invalidf("%s is no attribute of the definition", path).
				WithCode("configItemAttributeUnknown", path)
		}
	}

	for _, attribute := range attributes {
		occurrences := data[attribute.Key]
		path := parent.to(attribute.Key)

		if len(occurrences) < attribute.CountMin || len(occurrences) > attribute.CountMax {
			return apperror.Invalidf("%s occurs %d times, where the definition allows %d to %d", path.shown,
				len(occurrences), attribute.CountMin, attribute.CountMax).
				WithCode("configItemCountInvalid", path.shown, attribute.CountMin, attribute.CountMax)
		}

		for i, occurrence := range occurrences {
			at := path
			if attribute.CountMax > 1 {
				at = path.at(i)
			}

			kept := held[at.keys+"="+occurrence.Value]
			if err := s.checkValue(ctx, attribute, occurrence.Value, at.shown, kept); err != nil {
				return err
			}

			if err := s.checkData(ctx, attribute.Sub, occurrence.Sub, at, held); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkValue checks one value against the type of its attribute. kept says
// whether the version the item stands at holds the same value in the same
// place.
func (s *ConfigItemApplicationService) checkValue(
	ctx context.Context,
	attribute model.ClassAttribute,
	value string,
	path string,
	kept bool,
) error {
	if attribute.Type == model.AttributeSection {
		if value != "" {
			return refuseValue(path, attribute.Type)
		}

		return nil
	}

	if attribute.Required && strings.TrimSpace(value) == "" {
		return apperror.Invalidf("%s may not be empty", path).WithCode("configItemValueRequired", path)
	}

	if value == "" {
		return nil
	}

	limit := model.MaxAttributeValueLength
	if attribute.Type == model.AttributeText && attribute.MaxLength > 0 {
		limit = attribute.MaxLength
	}

	if model.TooLong(value, limit) {
		return apperror.Invalidf("%s is longer than %d characters", path, limit).
			WithCode("configItemValueTooLong", path, limit)
	}

	switch attribute.Type {
	case model.AttributeDate:
		if _, err := time.Parse(time.DateOnly, value); err != nil {
			return refuseValue(path, attribute.Type)
		}
	case model.AttributeDateTime:
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return refuseValue(path, attribute.Type)
		}
	case model.AttributeGeneralCatalog, model.AttributeCIClassReference, model.AttributeContact,
		model.AttributeOrganisation:
		// Written the one way an id is written, so that the same record is the
		// same value to the comparisons that decide "unchanged" and "kept".
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != value {
			return refuseValue(path, attribute.Type)
		}

		return s.checkReference(ctx, attribute, uint(id), path, kept)
	}

	return nil
}

// checkReference checks that an id names what its attribute points at, and
// that what it names is still offered unless it is kept where it stands.
func (s *ConfigItemApplicationService) checkReference(
	ctx context.Context,
	attribute model.ClassAttribute,
	id uint,
	path string,
	kept bool,
) error {
	switch attribute.Type {
	case model.AttributeGeneralCatalog:
		entry, err := s.entry(ctx, id, attribute.Catalog)
		if err != nil {
			return err
		}

		if entry == nil {
			return refuseValue(path, attribute.Type)
		}

		return stillOffered(entry, kept)
	case model.AttributeCIClassReference:
		item, err := s.items.GetByID(ctx, id)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return refuseValue(path, attribute.Type)
		}

		if err != nil {
			return err
		}

		class, err := s.catalog.GetByID(ctx, item.ClassID)
		if err != nil {
			return err
		}

		if !slices.Contains(attribute.Classes, class.Name) {
			return refuseValue(path, attribute.Type)
		}
	case model.AttributeContact:
		contact, err := s.contacts.GetByID(ctx, id)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return refuseValue(path, attribute.Type)
		}

		if err != nil {
			return err
		}

		if !contact.Valid && !kept {
			return apperror.Conflictf("the contact %q is no longer offered", contact.Name).
				WithCode("contactRetired", contact.Name)
		}
	case model.AttributeOrganisation:
		organisation, err := s.organisations.GetByID(ctx, id)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return refuseValue(path, attribute.Type)
		}

		if err != nil {
			return err
		}

		if !organisation.Valid && !kept {
			return apperror.Conflictf("the organisation %q is no longer offered", organisation.Name).
				WithCode("organisationRetired", organisation.Name)
		}
	}

	return nil
}

// refuseValue refuses a value that is not of its attribute's type.
func refuseValue(path string, kind model.AttributeType) error {
	return apperror.Invalidf("%s holds no value a %s can have", path, kind).
		WithCode("configItemValueInvalid", path, string(kind))
}

// joinPath names an attribute by the keys above it.
func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}

	return parent + "::" + key
}

// sameVersion reports whether a version says what another says, under the same
// definition.
func sameVersion(a, b *model.ConfigItemVersion) bool {
	if a.DefinitionID != b.DefinitionID || a.Name != b.Name || a.DeploymentStateID != b.DeploymentStateID ||
		a.IncidentStateID != b.IncidentStateID {
		return false
	}

	left, errLeft := json.Marshal(a.Data)
	right, errRight := json.Marshal(b.Data)

	return errLeft == nil && errRight == nil && string(left) == string(right)
}
