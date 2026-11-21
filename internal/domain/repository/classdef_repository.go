package repository

import (
	"context"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// ClassDefinitionRepository keeps the versions of the configuration item
// classes' definitions. A version is written once and never changed or
// deleted: an item is read under the version it was written with.
type ClassDefinitionRepository interface {
	// Current is the newest version of a class's definition, and not found where
	// the class has none yet.
	Current(ctx context.Context, classID uint) (*model.ClassDefinition, error)

	// Versions is every version of a class's definition, the newest first.
	Versions(ctx context.Context, classID uint) ([]*model.ClassDefinition, error)

	// Add writes a definition as its class's next version, numbered in the same
	// transaction, and answers it as stored.
	Add(ctx context.Context, definition *model.ClassDefinition) (*model.ClassDefinition, error)
}
