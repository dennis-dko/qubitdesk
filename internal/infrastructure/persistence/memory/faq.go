package memory

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// FAQRepository keeps the knowledge base in memory, its categories and its
// articles together, for the service tests.
type FAQRepository struct {
	mu          sync.Mutex
	categories  map[uint]model.FAQCategory
	articles    map[uint]model.FAQArticle
	nextGroup   uint
	nextArticle uint
}

// NewFAQRepository creates an empty knowledge base.
func NewFAQRepository() *FAQRepository {
	return &FAQRepository{
		categories: map[uint]model.FAQCategory{}, articles: map[uint]model.FAQArticle{}, nextGroup: 1, nextArticle: 1,
	}
}

// Categories is the repository's categories.
func (r *FAQRepository) Categories() repository.FAQCategoryRepository { return faqCategories{r} }

// Articles is the repository's articles.
func (r *FAQRepository) Articles() repository.FAQArticleRepository { return faqArticles{r} }

type faqCategories struct{ r *FAQRepository }

func (c faqCategories) GetAll(_ context.Context) ([]*model.FAQCategory, error) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()

	categories := make([]*model.FAQCategory, 0, len(c.r.categories))
	for _, category := range c.r.categories {
		copied := category
		categories = append(categories, &copied)
	}

	slices.SortFunc(categories, func(a, b *model.FAQCategory) int { return strings.Compare(a.Name, b.Name) })

	return categories, nil
}

func (c faqCategories) GetByID(_ context.Context, id uint) (*model.FAQCategory, error) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()

	category, ok := c.r.categories[id]
	if !ok {
		return nil, apperror.NotFound("faqcategory", strconv.FormatUint(uint64(id), 10))
	}

	return &category, nil
}

func (c faqCategories) Save(_ context.Context, category *model.FAQCategory) (*model.FAQCategory, error) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()

	if c.taken(category.Name, 0) {
		return nil, apperror.Conflictf("a category named %q already exists", category.Name).
			WithCode("faqCategoryNameTaken", category.Name)
	}

	stored := *category
	stored.ID = c.r.nextGroup
	c.r.nextGroup++
	c.r.categories[stored.ID] = stored

	return &stored, nil
}

func (c faqCategories) Update(_ context.Context, category *model.FAQCategory) (*model.FAQCategory, error) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()

	if _, ok := c.r.categories[category.ID]; !ok {
		return nil, apperror.NotFound("faqcategory", strconv.FormatUint(uint64(category.ID), 10))
	}

	if c.taken(category.Name, category.ID) {
		return nil, apperror.Conflictf("a category named %q already exists", category.Name).
			WithCode("faqCategoryNameTaken", category.Name)
	}

	c.r.categories[category.ID] = *category
	stored := c.r.categories[category.ID]

	return &stored, nil
}

func (c faqCategories) Delete(_ context.Context, id uint) error {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()

	if _, ok := c.r.categories[id]; !ok {
		return apperror.NotFound("faqcategory", strconv.FormatUint(uint64(id), 10))
	}

	delete(c.r.categories, id)

	return nil
}

func (c faqCategories) CountArticles(_ context.Context, id uint) (int, error) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()

	count := 0

	for _, article := range c.r.articles {
		if article.CategoryID == id {
			count++
		}
	}

	return count, nil
}

func (c faqCategories) CountChildren(_ context.Context, id uint) (int, error) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()

	count := 0

	for _, category := range c.r.categories {
		if category.ParentID != nil && *category.ParentID == id {
			count++
		}
	}

	return count, nil
}

func (c faqCategories) taken(name string, except uint) bool {
	for id, category := range c.r.categories {
		if id != except && category.Name == name {
			return true
		}
	}

	return false
}

type faqArticles struct{ r *FAQRepository }

func (a faqArticles) Search(_ context.Context, filter repository.FAQFilter) ([]*model.FAQArticle, error) {
	a.r.mu.Lock()
	defer a.r.mu.Unlock()

	words := strings.ToLower(strings.TrimSpace(filter.Words))
	found := make([]*model.FAQArticle, 0)

	for _, article := range a.r.articles {
		if !slices.Contains(filter.Visibilities, article.Visibility) ||
			(filter.CategoryID != 0 && article.CategoryID != filter.CategoryID) ||
			(filter.ValidOnly && !article.Valid) {
			continue
		}

		if words != "" && !strings.Contains(strings.ToLower(article.Title), words) &&
			!strings.Contains(strings.ToLower(article.Keywords), words) &&
			!strings.Contains(strings.ToLower(article.Body), words) {
			continue
		}

		copied := article
		found = append(found, &copied)
	}

	slices.SortFunc(found, func(x, y *model.FAQArticle) int {
		if c := y.UpdatedAt.Compare(x.UpdatedAt); c != 0 {
			return c
		}

		return int(y.ID) - int(x.ID)
	})

	if len(found) > filter.Limit {
		found = found[:filter.Limit]
	}

	return found, nil
}

func (a faqArticles) GetByID(_ context.Context, id uint) (*model.FAQArticle, error) {
	a.r.mu.Lock()
	defer a.r.mu.Unlock()

	article, ok := a.r.articles[id]
	if !ok {
		return nil, apperror.NotFound("faqarticle", strconv.FormatUint(uint64(id), 10))
	}

	return &article, nil
}

func (a faqArticles) Save(_ context.Context, article *model.FAQArticle) (*model.FAQArticle, error) {
	a.r.mu.Lock()
	defer a.r.mu.Unlock()

	stored := *article
	stored.ID = a.r.nextArticle
	a.r.nextArticle++
	a.r.articles[stored.ID] = stored

	return &stored, nil
}

func (a faqArticles) Update(_ context.Context, article *model.FAQArticle) (*model.FAQArticle, error) {
	a.r.mu.Lock()
	defer a.r.mu.Unlock()

	if _, ok := a.r.articles[article.ID]; !ok {
		return nil, apperror.NotFound("faqarticle", strconv.FormatUint(uint64(article.ID), 10))
	}

	a.r.articles[article.ID] = *article
	stored := a.r.articles[article.ID]

	return &stored, nil
}

func (a faqArticles) Delete(_ context.Context, id uint) error {
	a.r.mu.Lock()
	defer a.r.mu.Unlock()

	if _, ok := a.r.articles[id]; !ok {
		return apperror.NotFound("faqarticle", strconv.FormatUint(uint64(id), 10))
	}

	delete(a.r.articles, id)

	return nil
}
