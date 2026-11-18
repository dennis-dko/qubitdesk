package service

import (
	"context"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// GeneralCatalogApplicationService keeps the general catalog, as KIX18 keeps
// it: values are made and changed, a value no longer offered is retired, and
// nothing is deleted - what picked a value goes on naming it. A value's
// functionality is one its class allows, and none in a class whose values mean
// nothing to the code.
type GeneralCatalogApplicationService struct {
	items repository.GeneralCatalogRepository
}

// NewGeneralCatalogApplicationService works on the catalog alone.
func NewGeneralCatalogApplicationService(items repository.GeneralCatalogRepository) *GeneralCatalogApplicationService {
	return &GeneralCatalogApplicationService{items: items}
}

// Items is the values of a class by name, or of every class where it is empty.
func (s *GeneralCatalogApplicationService) Items(ctx context.Context, class string) ([]*model.CatalogItem, error) {
	return s.items.Items(ctx, strings.TrimSpace(class))
}

// Classes is every class a value is kept under, by name.
func (s *GeneralCatalogApplicationService) Classes(ctx context.Context) ([]string, error) {
	return s.items.Classes(ctx)
}

// Create makes a value, in a class of its own where none holds the name yet:
// a class is a list somebody needs, and making its first value makes it.
func (s *GeneralCatalogApplicationService) Create(
	ctx context.Context,
	cmd command.CatalogItemCommand,
) (*model.CatalogItem, error) {
	item := &model.CatalogItem{Class: strings.TrimSpace(valueOf(cmd.Class)), Valid: true}

	if item.Class == "" || model.TooLong(item.Class, model.MaxCatalogClassLength) {
		return nil, apperror.InvalidFields("class")
	}

	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	if err := applyCatalogItem(item, cmd); err != nil {
		return nil, err
	}

	return s.items.Save(ctx, item)
}

// Update changes a value; its class stays the one it was made in.
func (s *GeneralCatalogApplicationService) Update(
	ctx context.Context,
	id uint,
	cmd command.CatalogItemCommand,
) (*model.CatalogItem, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	item, err := s.items.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if cmd.Class != nil && strings.TrimSpace(*cmd.Class) != item.Class {
		return nil, apperror.InvalidFields("class")
	}

	if err := applyCatalogItem(item, cmd); err != nil {
		return nil, err
	}

	return s.items.Update(ctx, item)
}

// applyCatalogItem writes what cmd says onto item and refuses what it cannot be
// kept with.
func applyCatalogItem(item *model.CatalogItem, cmd command.CatalogItemCommand) error {
	if cmd.Name != nil {
		item.Name = strings.TrimSpace(*cmd.Name)
	}

	if cmd.Functionality != nil {
		item.Functionality = strings.TrimSpace(*cmd.Functionality)
	}

	if cmd.Comment != nil {
		item.Comment = strings.TrimSpace(*cmd.Comment)
	}

	if cmd.Valid != nil {
		item.Valid = *cmd.Valid
	}

	var invalid []string

	if item.Name == "" || model.TooLong(item.Name, model.MaxNameLength) {
		invalid = append(invalid, "name")
	}

	if !model.FunctionalityOf(item.Class, item.Functionality) {
		invalid = append(invalid, "functionality")
	}

	if model.TooLong(item.Comment, model.MaxDescriptionLength) {
		invalid = append(invalid, "comment")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return nil
}
