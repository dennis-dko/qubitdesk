package service

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ClassDefinitionApplicationService keeps what a configuration item of each
// class holds, as KIX18 defines its classes: a class is a value of the general
// catalog's ITSM::ConfigItem::Class, and its definition is a list of attributes
// written as a new version each time it changes - never changed in place,
// because an item is read under the version it was written with.
//
// A definition is checked whole before it is kept: every key, type, count and
// what a reference names, so that a definition that saves is one an item can
// be written under.
type ClassDefinitionApplicationService struct {
	catalog     repository.GeneralCatalogRepository
	definitions repository.ClassDefinitionRepository
	now         func() time.Time
}

// NewClassDefinitionApplicationService reads the classes from the general
// catalog and keeps their definitions.
func NewClassDefinitionApplicationService(
	catalog repository.GeneralCatalogRepository,
	definitions repository.ClassDefinitionRepository,
) *ClassDefinitionApplicationService {
	return &ClassDefinitionApplicationService{catalog: catalog, definitions: definitions, now: time.Now}
}

// ClassWithDefinition is a class of configuration items and the version of its
// definition that stands now, 0 where it has none yet.
type ClassWithDefinition struct {
	Class   *model.CatalogItem
	Version int
}

// Classes is every class a configuration item can be of, retired ones
// included, each with the version its definition stands at.
func (s *ClassDefinitionApplicationService) Classes(ctx context.Context) ([]ClassWithDefinition, error) {
	classes, err := s.catalog.Items(ctx, model.CatalogCIClass)
	if err != nil {
		return nil, err
	}

	out := make([]ClassWithDefinition, 0, len(classes))

	for _, class := range classes {
		entry := ClassWithDefinition{Class: class}

		current, err := s.definitions.Current(ctx, class.ID)

		switch {
		case err == nil:
			entry.Version = current.Version
		case apperror.KindOf(err) != apperror.KindNotFound:
			return nil, err
		}

		out = append(out, entry)
	}

	return out, nil
}

// Versions is every version of a class's definition, the newest first.
func (s *ClassDefinitionApplicationService) Versions(ctx context.Context, classID uint) ([]*model.ClassDefinition, error) {
	if _, err := s.class(ctx, classID); err != nil {
		return nil, err
	}

	return s.definitions.Versions(ctx, classID)
}

// Add writes a class's definition as its next version, by the account given.
// It is refused when it says nothing the version standing now does not.
func (s *ClassDefinitionApplicationService) Add(
	ctx context.Context,
	author uint,
	classID uint,
	attributes []model.ClassAttribute,
) (*model.ClassDefinition, error) {
	if _, err := s.class(ctx, classID); err != nil {
		return nil, err
	}

	if err := s.check(ctx, attributes); err != nil {
		return nil, err
	}

	current, err := s.definitions.Current(ctx, classID)

	switch {
	case err == nil:
		if sameAttributes(current.Attributes, attributes) {
			return nil, apperror.Invalidf("the definition says what version %d says", current.Version).
				WithCode("classDefinitionUnchanged", current.Version)
		}
	case apperror.KindOf(err) != apperror.KindNotFound:
		return nil, err
	}

	return s.definitions.Add(ctx, &model.ClassDefinition{
		ClassID: classID, Attributes: attributes, CreatedAt: s.now().UTC(), CreatedBy: author,
	})
}

// class is the configuration item class by its id, and not found for an id
// that names no value or a value of another list.
func (s *ClassDefinitionApplicationService) class(ctx context.Context, id uint) (*model.CatalogItem, error) {
	class, err := s.catalog.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if class.Class != model.CatalogCIClass {
		return nil, apperror.NotFound("catalogitem", strconv.FormatUint(uint64(id), 10))
	}

	return class, nil
}

// attributeKey is a key KIX18 accepts: a letter, then letters and digits.
var attributeKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// check refuses a definition an item could not be written under, naming the
// attribute by the path of its keys.
func (s *ClassDefinitionApplicationService) check(ctx context.Context, attributes []model.ClassAttribute) error {
	if len(attributes) == 0 {
		return apperror.Invalidf("a definition holds at least one attribute").WithCode("classDefinitionEmpty")
	}

	total := 0
	if err := countAttributes(attributes, &total); err != nil {
		return err
	}

	ciClasses, err := s.catalog.Items(ctx, model.CatalogCIClass)
	if err != nil {
		return err
	}

	lists, err := s.catalog.Classes(ctx)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(ciClasses))
	for _, class := range ciClasses {
		names = append(names, class.Name)
	}

	return checkAttributes(attributes, "", 1, names, lists)
}

// countAttributes bounds how many attributes a definition holds altogether.
func countAttributes(attributes []model.ClassAttribute, total *int) error {
	for _, attribute := range attributes {
		*total++
		if *total > model.MaxClassAttributes {
			return apperror.Invalidf("a definition holds at most %d attributes", model.MaxClassAttributes).
				WithCode("classDefinitionTooLarge", model.MaxClassAttributes)
		}

		if err := countAttributes(attribute.Sub, total); err != nil {
			return err
		}
	}

	return nil
}

// checkAttributes checks one level of attributes and what is below them.
func checkAttributes(attributes []model.ClassAttribute, parent string, depth int, ciClasses, lists []string) error {
	seen := map[string]bool{}

	for _, attribute := range attributes {
		path := attribute.Key
		if parent != "" {
			path = parent + "::" + attribute.Key
		}

		if !attributeKey.MatchString(attribute.Key) || len(attribute.Key) > model.MaxAttributeKeyLen {
			return apperror.Invalidf("%q is no key an attribute can have", path).WithCode("attributeKeyInvalid", path)
		}

		if seen[attribute.Key] {
			return apperror.Invalidf("%s is the key of two attributes", path).WithCode("attributeKeyTwice", path)
		}

		seen[attribute.Key] = true

		if strings.TrimSpace(attribute.Name) == "" || model.TooLong(attribute.Name, model.MaxNameLength) {
			return apperror.Invalidf("%s has no name a screen can show", path).WithCode("attributeNameInvalid", path)
		}

		if err := checkAttribute(attribute, path, ciClasses, lists); err != nil {
			return err
		}

		if len(attribute.Sub) > 0 && depth >= model.MaxAttributeDepth {
			return apperror.Invalidf("%s nests deeper than %d levels", path, model.MaxAttributeDepth).
				WithCode("attributeTooDeep", path, model.MaxAttributeDepth)
		}

		if err := checkAttributes(attribute.Sub, path, depth+1, ciClasses, lists); err != nil {
			return err
		}
	}

	return nil
}

// checkAttribute checks what one attribute says of itself: its type, how often
// it occurs, and what its type needs named.
func checkAttribute(attribute model.ClassAttribute, path string, ciClasses, lists []string) error {
	if !slices.Contains(model.AttributeTypes(), attribute.Type) {
		return apperror.Invalidf("%s is of no type a definition knows: %q", path, attribute.Type).
			WithCode("attributeTypeUnknown", path, string(attribute.Type))
	}

	least, most, begin := attribute.CountMin, attribute.CountMax, attribute.CountDefault
	if least < 0 || most < 1 || most > model.MaxAttributeCount || least > most || begin < least || begin > most {
		return apperror.Invalidf("%s occurs at least %d, at most %d and at first %d times", path, least, most, begin).
			WithCode("attributeCountInvalid", path)
	}

	if attribute.Type == model.AttributeSection {
		if most != 1 || len(attribute.Sub) == 0 {
			return apperror.Invalidf("%s is a section, which occurs once and holds attributes", path).
				WithCode("attributeSectionInvalid", path)
		}

		return nil
	}

	switch {
	case attribute.Type == model.AttributeText &&
		(attribute.MaxLength < 0 || attribute.MaxLength > model.MaxAttributeTextLen):
		return apperror.Invalidf("%s is a text of up to %d characters", path, model.MaxAttributeTextLen).
			WithCode("attributeLengthInvalid", path, model.MaxAttributeTextLen)
	case attribute.Type != model.AttributeText && attribute.MaxLength != 0:
		return apperror.Invalidf("%s is no text, so it has no length", path).WithCode("attributeLengthInvalid", path,
			model.MaxAttributeTextLen)
	}

	if attribute.Type == model.AttributeGeneralCatalog {
		if !slices.Contains(lists, attribute.Catalog) {
			return apperror.Invalidf("%s picks from %q, which the general catalog does not hold", path,
				attribute.Catalog).WithCode("attributeCatalogUnknown", path, attribute.Catalog)
		}
	} else if attribute.Catalog != "" {
		return apperror.Invalidf("%s picks from no list", path).WithCode("attributeCatalogUnknown", path,
			attribute.Catalog)
	}

	if attribute.Type == model.AttributeCIClassReference {
		if len(attribute.Classes) == 0 {
			return apperror.Invalidf("%s names no class an item it points at can be of", path).
				WithCode("attributeClassUnknown", path, "")
		}

		for _, class := range attribute.Classes {
			if !slices.Contains(ciClasses, class) {
				return apperror.Invalidf("%s points at items of %q, which is no class", path, class).
					WithCode("attributeClassUnknown", path, class)
			}
		}
	} else if len(attribute.Classes) > 0 {
		return apperror.Invalidf("%s points at no item", path).WithCode("attributeClassUnknown", path,
			attribute.Classes[0])
	}

	return nil
}

// sameAttributes reports whether two definitions say the same.
func sameAttributes(a, b []model.ClassAttribute) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)

	return errLeft == nil && errRight == nil && string(left) == string(right)
}
