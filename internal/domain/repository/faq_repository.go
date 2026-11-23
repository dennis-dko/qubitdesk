package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// FAQCategoryRepository keeps the knowledge base's categories.
type FAQCategoryRepository interface {
	GetAll(ctx context.Context) ([]*model.FAQCategory, error)
	GetByID(ctx context.Context, id uint) (*model.FAQCategory, error)
	Save(ctx context.Context, category *model.FAQCategory) (*model.FAQCategory, error)
	Update(ctx context.Context, category *model.FAQCategory) (*model.FAQCategory, error)
	Delete(ctx context.Context, id uint) error

	// CountArticles reports how many articles are filed under the category.
	CountArticles(ctx context.Context, id uint) (int, error)

	// CountChildren reports how many categories sit directly under this one.
	CountChildren(ctx context.Context, id uint) (int, error)
}

// FAQArticleRepository keeps the knowledge base's articles.
type FAQArticleRepository interface {
	// Search is the articles a filter takes, the latest written first.
	Search(ctx context.Context, filter FAQFilter) ([]*model.FAQArticle, error)
	GetByID(ctx context.Context, id uint) (*model.FAQArticle, error)
	Save(ctx context.Context, article *model.FAQArticle) (*model.FAQArticle, error)
	Update(ctx context.Context, article *model.FAQArticle) (*model.FAQArticle, error)
	Delete(ctx context.Context, id uint) error
}

// FAQFilter says which articles a search takes.
type FAQFilter struct {
	// Words are looked for in the title, the keywords and the text; nothing takes
	// every article.
	Words string

	// CategoryID takes the articles of one category alone; nought takes all.
	CategoryID uint

	// Visibilities are the ones the reader may see. None is no article: the
	// filter never widens to every visibility by being left empty.
	Visibilities []model.FAQVisibility

	// ValidOnly leaves the retired articles out.
	ValidOnly bool

	// Limit bounds how many are handed out.
	Limit int
}
