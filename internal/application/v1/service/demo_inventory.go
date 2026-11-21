package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/i18n"
)

// demoInventory is what the demo's configuration items are written through, the
// catalog their classes, states and lists are found in by name, and what links a
// ticket to the item it is about.
type demoInventory struct {
	items   *ConfigItemApplicationService
	catalog repository.GeneralCatalogRepository
	links   *LinkApplicationService
}

// seedInventory adds the configuration items, each under its class's definition
// as it stands, in production and operational, and answers them by their keys.
func (s *DemoService) seedInventory(
	ctx context.Context,
	caller *Principal,
	t i18n.Translator,
	contacts map[string]*model.Contact,
	organisations map[string]uint,
	summary *DemoSummary,
) (map[string]uint, error) {
	if s.inventory == nil {
		return map[string]uint{}, nil
	}

	find := func(class, name string) (uint, error) {
		entries, err := s.inventory.catalog.Items(ctx, class)
		if err != nil {
			return 0, err
		}

		for _, entry := range entries {
			if entry.Name == name {
				return entry.ID, nil
			}
		}

		return 0, fmt.Errorf("the catalog ships no %s %s", class, name)
	}

	deployment, err := find(model.CatalogDeploymentState, "production")
	if err != nil {
		return nil, err
	}

	incident, err := find(model.CatalogIncidentState, "operational")
	if err != nil {
		return nil, err
	}

	marks := demoMarks{find: find, contacts: contacts, organisations: organisations, items: map[string]uint{},
		today: s.now()}

	for _, item := range demoItems {
		class, err := find(model.CatalogCIClass, item.class)
		if err != nil {
			return nil, err
		}

		definition, err := s.inventory.items.Definition(ctx, class)
		if err != nil {
			return nil, fmt.Errorf("reading the definition of %s: %w", item.class, err)
		}

		data, err := marks.data(definition.Attributes, item.values, "")
		if err != nil {
			return nil, fmt.Errorf("writing the values of %s: %w", item.key, err)
		}

		made, err := s.inventory.items.Create(ctx, caller.User.ID, class, ConfigItemVersionCommand{
			Name: item.name.in(t), DeploymentStateID: deployment, IncidentStateID: incident, Data: data,
		})
		if err != nil {
			return nil, fmt.Errorf("adding the item %s: %w", item.key, err)
		}

		marks.items[item.key] = made.Item.ID
		summary.ConfigItems++
	}

	return marks.items, nil
}

// linkAbout links a demo ticket to the item it is about, where the demo has
// that item.
func (s *DemoService) linkAbout(ctx context.Context, caller *Principal, ticketID uint, item uint) error {
	if s.inventory == nil || item == 0 {
		return nil
	}

	_, err := s.inventory.links.Create(ctx, caller, LinkCommand{
		SourceObject: model.LinkTicket, SourceID: ticketID, TargetObject: model.LinkConfigItem, TargetID: item,
		Type: model.LinkNormal,
	})

	return err
}

// demoMarks reads the marks an item's values name records by.
type demoMarks struct {
	find          func(class, name string) (uint, error)
	contacts      map[string]*model.Contact
	organisations map[string]uint
	items         map[string]uint
	today         time.Time
}

// data is what an item holds under attributes: every attribute as often as it
// has to occur, and once where a value is given for it or beneath it.
func (m demoMarks) data(attributes []model.ClassAttribute, given map[string]string, parent string) (
	model.AttributeData, error,
) {
	data := model.AttributeData{}

	for _, attribute := range attributes {
		path := joinPath(parent, attribute.Key)
		raw, named := given[path]
		count := attribute.CountMin

		if count == 0 && (named || givenBeneath(given, path)) {
			count = 1
		}

		for i := range count {
			var occurrence model.AttributeValue

			if i == 0 && named {
				value, err := m.value(attribute, raw)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}

				occurrence.Value = value
			}

			if len(attribute.Sub) > 0 {
				sub, err := m.data(attribute.Sub, given, path)
				if err != nil {
					return nil, err
				}

				occurrence.Sub = sub
			}

			data[attribute.Key] = append(data[attribute.Key], occurrence)
		}
	}

	return data, nil
}

// givenBeneath reports whether a value is given beneath the attribute at path.
func givenBeneath(given map[string]string, path string) bool {
	for key := range given {
		if strings.HasPrefix(key, path+"::") {
			return true
		}
	}

	return false
}

// value is a value as written, or the id or day its mark names.
func (m demoMarks) value(attribute model.ClassAttribute, raw string) (string, error) {
	mark, name, marked := strings.Cut(strings.TrimPrefix(raw, "@"), ":")
	if !strings.HasPrefix(raw, "@") || !marked {
		return raw, nil
	}

	id := func(found uint, ok bool) (string, error) {
		if !ok {
			return "", fmt.Errorf("the demo has no %s %s", mark, name)
		}

		return strconv.FormatUint(uint64(found), 10), nil
	}

	switch mark {
	case "contact":
		contact, ok := m.contacts[name]
		if !ok {
			return id(0, false)
		}

		return id(contact.ID, true)
	case "organisation":
		found, ok := m.organisations[name]

		return id(found, ok)
	case "item":
		found, ok := m.items[name]

		return id(found, ok)
	case "list":
		found, err := m.find(attribute.Catalog, name)
		if err != nil {
			return "", err
		}

		return id(found, true)
	case "day":
		days, err := strconv.Atoi(name)
		if err != nil {
			return "", fmt.Errorf("%q is no number of days", name)
		}

		return m.today.AddDate(0, 0, days).Format(time.DateOnly), nil
	}

	return "", fmt.Errorf("%q is no mark the demo reads", mark)
}
