package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// ClassDefinitionHandler serves what a configuration item of each class holds:
// read by every account signed in, because whatever shows an item has to know
// what its values are, and written with catalog:write, as the general catalog
// the classes are kept in is.
type ClassDefinitionHandler struct {
	definitions *service.ClassDefinitionApplicationService
	authz       *Authorizer
}

// NewClassDefinitionHandler wires the handler.
func NewClassDefinitionHandler(
	definitions *service.ClassDefinitionApplicationService,
	authz *Authorizer,
) *ClassDefinitionHandler {
	return &ClassDefinitionHandler{definitions: definitions, authz: authz}
}

// CIClassResponse is a class of configuration items and the version its
// definition stands at, 0 where it has none yet.
type CIClassResponse struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Valid   bool   `json:"valid"`
	Version int    `json:"version"`
}

// ClassDefinitionResponse is one version of a class's definition.
//
// The attributes travel as they are kept, under the names model.ClassAttribute
// gives them: the definition is the document an administrator writes, and a
// second shape of it on the wire would be a second thing to keep in step.
type ClassDefinitionResponse struct {
	ID         uint                   `json:"id"`
	ClassID    uint                   `json:"classId"`
	Version    int                    `json:"version"`
	Attributes []model.ClassAttribute `json:"attributes"`
	CreatedAt  time.Time              `json:"createdAt"`
	CreatedBy  uint                   `json:"createdBy"`
}

// ClassDefinitionRequest writes a class's next version.
type ClassDefinitionRequest struct {
	Attributes []model.ClassAttribute `json:"attributes"`
}

func newClassDefinitionResponse(definition *model.ClassDefinition) ClassDefinitionResponse {
	attributes := definition.Attributes
	if attributes == nil {
		attributes = []model.ClassAttribute{}
	}

	return ClassDefinitionResponse{
		ID: definition.ID, ClassID: definition.ClassID, Version: definition.Version, Attributes: attributes,
		CreatedAt: definition.CreatedAt, CreatedBy: definition.CreatedBy,
	}
}

// Classes handles GET /api/v1/cmdb/classes.
func (h *ClassDefinitionHandler) Classes(c *gofr.Context) (any, error) {
	if _, err := h.authz.permittedPrincipal(c); err != nil {
		return nil, err
	}

	classes, err := h.definitions.Classes(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listResponse[CIClassResponse]{Items: make([]CIClassResponse, 0, len(classes))}
	for _, entry := range classes {
		page.Items = append(page.Items, CIClassResponse{ID: entry.Class.ID, Name: entry.Class.Name,
			Valid: entry.Class.Valid, Version: entry.Version})
	}

	page.TotalCount = uint(len(page.Items))

	return page, nil
}

// Definitions handles GET /api/v1/cmdb/classes/{id}/definitions: every version
// of the class's definition, the newest first.
func (h *ClassDefinitionHandler) Definitions(c *gofr.Context) (any, error) {
	if _, err := h.authz.permittedPrincipal(c); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	versions, err := h.definitions.Versions(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(versions, newClassDefinitionResponse), nil
}

// AddDefinition handles POST /api/v1/cmdb/classes/{id}/definitions: the
// class's next version. There is no PUT and no DELETE - a version is never
// changed, because an item is read under the one it was written with.
func (h *ClassDefinitionHandler) AddDefinition(c *gofr.Context) (any, error) {
	principal, err := h.authz.Require(c, model.PermCatalogWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req ClassDefinitionRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	definition, err := h.definitions.Add(c, principal.User.ID, id, req.Attributes)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newClassDefinitionResponse(definition), nil
}
