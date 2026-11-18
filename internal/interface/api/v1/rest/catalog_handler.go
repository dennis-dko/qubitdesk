package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// GeneralCatalogHandler serves the general catalog: read by every account
// signed in, because every screen offering one of its values has to say what
// they are, and changed with catalog:write.
type GeneralCatalogHandler struct {
	catalog *service.GeneralCatalogApplicationService
	authz   *Authorizer
}

// NewGeneralCatalogHandler wires the handler.
func NewGeneralCatalogHandler(catalog *service.GeneralCatalogApplicationService, authz *Authorizer) *GeneralCatalogHandler {
	return &GeneralCatalogHandler{catalog: catalog, authz: authz}
}

// CatalogItemResponse is a value of the general catalog.
type CatalogItemResponse struct {
	ID            uint   `json:"id"`
	Class         string `json:"class"`
	Name          string `json:"name"`
	Functionality string `json:"functionality"`
	Comment       string `json:"comment"`
	Valid         bool   `json:"valid"`
}

// CatalogClassResponse is a class of the general catalog, with what a value of
// it may mean to the code - nothing for most.
type CatalogClassResponse struct {
	Name            string   `json:"name"`
	Functionalities []string `json:"functionalities"`
}

// CatalogItemRequest makes a value or changes one; on a change an absent field
// stays as it was.
type CatalogItemRequest struct {
	Class         *string `json:"class"`
	Name          *string `json:"name"`
	Functionality *string `json:"functionality"`
	Comment       *string `json:"comment"`
	Valid         *bool   `json:"valid"`
}

func newCatalogItemResponse(item *model.CatalogItem) CatalogItemResponse {
	return CatalogItemResponse{
		ID: item.ID, Class: item.Class, Name: item.Name, Functionality: item.Functionality,
		Comment: item.Comment, Valid: item.Valid,
	}
}

func (r CatalogItemRequest) command() command.CatalogItemCommand {
	return command.CatalogItemCommand{
		Class: r.Class, Name: r.Name, Functionality: r.Functionality, Comment: r.Comment, Valid: r.Valid,
	}
}

// Items handles GET /api/v1/general-catalog: the values of the class asked for
// with ?class=, or of every class.
func (h *GeneralCatalogHandler) Items(c *gofr.Context) (any, error) {
	if _, err := h.authz.permittedPrincipal(c); err != nil {
		return nil, err
	}

	items, err := h.catalog.Items(c, c.Param("class"))
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(items, newCatalogItemResponse), nil
}

// Classes handles GET /api/v1/general-catalog/classes.
func (h *GeneralCatalogHandler) Classes(c *gofr.Context) (any, error) {
	if _, err := h.authz.permittedPrincipal(c); err != nil {
		return nil, err
	}

	classes, err := h.catalog.Classes(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listResponse[CatalogClassResponse]{Items: make([]CatalogClassResponse, 0, len(classes))}
	for _, class := range classes {
		functionalities := model.Functionalities(class)
		if functionalities == nil {
			functionalities = []string{}
		}

		page.Items = append(page.Items, CatalogClassResponse{Name: class, Functionalities: functionalities})
	}

	page.TotalCount = uint(len(page.Items))

	return page, nil
}

// Create handles POST /api/v1/general-catalog.
func (h *GeneralCatalogHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCatalogWrite); err != nil {
		return nil, err
	}

	var req CatalogItemRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	item, err := h.catalog.Create(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newCatalogItemResponse(item), nil
}

// Update handles PUT /api/v1/general-catalog/{id}. There is no DELETE: a value
// no longer offered is retired, as KIX18 retires it.
func (h *GeneralCatalogHandler) Update(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCatalogWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req CatalogItemRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	item, err := h.catalog.Update(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newCatalogItemResponse(item), nil
}
