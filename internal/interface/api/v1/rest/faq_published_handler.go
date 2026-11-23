package rest

import (
	"errors"
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// PublishedArticleResponse is an article as somebody outside the staff reads
// it: what it says and where it is filed, and nothing of who wrote it or who
// else may read it.
type PublishedArticleResponse struct {
	ID         uint      `json:"id"`
	CategoryID uint      `json:"categoryId"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Keywords   string    `json:"keywords"`
	Language   string    `json:"language"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// PublishedCategoryResponse is a category somebody outside the staff is shown.
type PublishedCategoryResponse struct {
	ID       uint   `json:"id"`
	ParentID *uint  `json:"parentId"`
	Name     string `json:"name"`
}

func newPublishedArticleResponse(article *model.FAQArticle) PublishedArticleResponse {
	return PublishedArticleResponse{
		ID: article.ID, CategoryID: article.CategoryID, Title: article.Title, Body: article.Body,
		Keywords: article.Keywords, Language: article.Language, UpdatedAt: article.UpdatedAt,
	}
}

// reader is who asks, and the routes below refuse nobody for asking: a customer
// signed in to the portal reads what is meant for customers, and anybody else -
// somebody signed in to nothing, an account without the portal, or one whose
// initial password is still to be replaced, which may use nothing else either -
// reads what is public.
func (h *FAQHandler) reader(c *gofr.Context) (service.FAQReader, error) {
	principal, err := h.authz.Principal(c)

	var nobody unauthorizedError

	switch {
	case errors.As(err, &nobody):
		return service.Anybody, nil
	case err != nil:
		return service.Anybody, err
	case principal.User != nil && principal.User.MustChangePassword:
		return service.Anybody, nil
	case principal.Can(model.PermPortalUse):
		return service.Customer, nil
	}

	return service.Anybody, nil
}

// PublishedArticles handles GET /api/v1/portal/faq/articles: what a search by
// words and by category takes of the articles the caller may read from outside
// the staff, the latest written first.
func (h *FAQHandler) PublishedArticles(c *gofr.Context) (any, error) {
	reader, err := h.reader(c)
	if err != nil {
		return nil, err
	}

	category, err := queryUint(c, "category")
	if err != nil {
		return nil, toHTTPError(err)
	}

	articles, err := h.faq.Published(c, reader, c.Param("q"), category)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(articles, newPublishedArticleResponse), nil
}

// PublishedArticle handles GET /api/v1/portal/faq/articles/{id}.
func (h *FAQHandler) PublishedArticle(c *gofr.Context) (any, error) {
	reader, err := h.reader(c)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	article, err := h.faq.PublishedArticle(c, reader, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newPublishedArticleResponse(article), nil
}

// PublishedCategories handles GET /api/v1/portal/faq/categories: the categories
// holding something the caller may read, and the ones above those.
func (h *FAQHandler) PublishedCategories(c *gofr.Context) (any, error) {
	reader, err := h.reader(c)
	if err != nil {
		return nil, err
	}

	categories, err := h.faq.PublishedCategories(c, reader)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(categories, func(category *model.FAQCategory) PublishedCategoryResponse {
		return PublishedCategoryResponse{ID: category.ID, ParentID: category.ParentID, Name: category.Name}
	}), nil
}
