package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// FAQHandler serves the knowledge base: read by whoever reads it, and written,
// categories included, by whoever writes it.
type FAQHandler struct {
	faq   *service.FAQApplicationService
	authz *Authorizer
}

// NewFAQHandler wires the handler.
func NewFAQHandler(faq *service.FAQApplicationService, authz *Authorizer) *FAQHandler {
	return &FAQHandler{faq: faq, authz: authz}
}

// FAQCategoryResponse is a category as the API hands it out.
type FAQCategoryResponse struct {
	ID          uint   `json:"id"`
	ParentID    *uint  `json:"parentId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Valid       bool   `json:"valid"`
}

// FAQCategoryRequest creates or changes a category. On a change an absent field
// stays as it was; ClearParent puts the category at the top again.
type FAQCategoryRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	ParentID    *uint   `json:"parentId"`
	ClearParent bool    `json:"clearParent"`
	Valid       *bool   `json:"valid"`
}

// FAQArticleResponse is an article as the API hands it out, with the name of
// whoever last wrote it.
type FAQArticleResponse struct {
	ID            uint      `json:"id"`
	CategoryID    uint      `json:"categoryId"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	Keywords      string    `json:"keywords"`
	Visibility    string    `json:"visibility"`
	Language      string    `json:"language"`
	Valid         bool      `json:"valid"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	UpdatedBy     *uint     `json:"updatedBy"`
	UpdatedByName string    `json:"updatedByName"`
}

// FAQArticleListResponse is the articles a search took, and the visibilities an
// article can have, so a form offers what the server takes.
type FAQArticleListResponse struct {
	Items        []FAQArticleResponse `json:"items"`
	TotalCount   uint                 `json:"totalCount"`
	Visibilities []string             `json:"visibilities"`
}

// FAQArticleRequest creates or changes an article. On a change an absent field
// stays as it was.
type FAQArticleRequest struct {
	CategoryID *uint   `json:"categoryId"`
	Title      *string `json:"title"`
	Body       *string `json:"body"`
	Keywords   *string `json:"keywords"`
	Visibility *string `json:"visibility"`
	Language   *string `json:"language"`
	Valid      *bool   `json:"valid"`
}

func newFAQCategoryResponse(category *model.FAQCategory) FAQCategoryResponse {
	return FAQCategoryResponse{
		ID: category.ID, ParentID: category.ParentID, Name: category.Name, Description: category.Description,
		Valid: category.Valid,
	}
}

func newFAQArticleResponse(article *model.FAQArticle, names map[uint]string) FAQArticleResponse {
	response := FAQArticleResponse{
		ID: article.ID, CategoryID: article.CategoryID, Title: article.Title, Body: article.Body,
		Keywords: article.Keywords, Visibility: string(article.Visibility), Language: article.Language,
		Valid: article.Valid, CreatedAt: article.CreatedAt, UpdatedAt: article.UpdatedAt, UpdatedBy: article.UpdatedBy,
	}

	if article.UpdatedBy != nil {
		response.UpdatedByName = names[*article.UpdatedBy]
	}

	return response
}

// Categories handles GET /api/v1/faq/categories.
func (h *FAQHandler) Categories(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermFAQRead); err != nil {
		return nil, err
	}

	categories, err := h.faq.Categories(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(categories, newFAQCategoryResponse), nil
}

// CreateCategory handles POST /api/v1/faq/categories.
func (h *FAQHandler) CreateCategory(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermFAQWrite); err != nil {
		return nil, err
	}

	var req FAQCategoryRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	category, err := h.faq.CreateCategory(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newFAQCategoryResponse(category), nil
}

// UpdateCategory handles PUT /api/v1/faq/categories/{id}.
func (h *FAQHandler) UpdateCategory(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermFAQWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req FAQCategoryRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	category, err := h.faq.UpdateCategory(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newFAQCategoryResponse(category), nil
}

// DeleteCategory handles DELETE /api/v1/faq/categories/{id}.
func (h *FAQHandler) DeleteCategory(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermFAQWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.faq.DeleteCategory(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}

// Articles handles GET /api/v1/faq/articles?q=&category=: the articles the
// reader may read that the words and the category take, the latest written
// first.
func (h *FAQHandler) Articles(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermFAQRead)
	if err != nil {
		return nil, err
	}

	category, err := queryUint(c, "category")
	if err != nil {
		return nil, toHTTPError(err)
	}

	articles, err := h.faq.Search(c, caller, c.Param("q"), category)
	if err != nil {
		return nil, toHTTPError(err)
	}

	names, err := h.faq.Names(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	items := make([]FAQArticleResponse, 0, len(articles))
	for _, article := range articles {
		items = append(items, newFAQArticleResponse(article, names))
	}

	visibilities := make([]string, 0)
	for _, visibility := range model.FAQVisibilities() {
		visibilities = append(visibilities, string(visibility))
	}

	return FAQArticleListResponse{Items: items, TotalCount: uint(len(items)), Visibilities: visibilities}, nil
}

// Article handles GET /api/v1/faq/articles/{id}.
func (h *FAQHandler) Article(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermFAQRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	article, err := h.faq.Article(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respond(c, article)
}

// CreateArticle handles POST /api/v1/faq/articles.
func (h *FAQHandler) CreateArticle(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermFAQWrite)
	if err != nil {
		return nil, err
	}

	var req FAQArticleRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	article, err := h.faq.CreateArticle(c, caller, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respond(c, article)
}

// UpdateArticle handles PUT /api/v1/faq/articles/{id}.
func (h *FAQHandler) UpdateArticle(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermFAQWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req FAQArticleRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	article, err := h.faq.UpdateArticle(c, caller, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respond(c, article)
}

// DeleteArticle handles DELETE /api/v1/faq/articles/{id}.
func (h *FAQHandler) DeleteArticle(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermFAQWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.faq.DeleteArticle(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}

// respond is one article as the API hands it out, its last writer named.
func (h *FAQHandler) respond(c *gofr.Context, article *model.FAQArticle) (any, error) {
	names, err := h.faq.Names(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newFAQArticleResponse(article, names), nil
}

func (r FAQCategoryRequest) command() command.FAQCategoryCommand {
	return command.FAQCategoryCommand{
		Name: r.Name, Description: r.Description, ParentID: r.ParentID, ClearParent: r.ClearParent, Valid: r.Valid,
	}
}

func (r FAQArticleRequest) command() command.FAQArticleCommand {
	return command.FAQArticleCommand{
		CategoryID: r.CategoryID, Title: r.Title, Body: r.Body, Keywords: r.Keywords, Visibility: r.Visibility,
		Language: r.Language, Valid: r.Valid,
	}
}
