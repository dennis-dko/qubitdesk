package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// ConfigItemHandler serves the configuration items: read with assets:read and
// written with assets:write, every item whatever its class - an item is the
// organisation's inventory, not narrowed like a queue's tickets.
type ConfigItemHandler struct {
	items *service.ConfigItemApplicationService
	authz *Authorizer
}

// NewConfigItemHandler wires the handler.
func NewConfigItemHandler(items *service.ConfigItemApplicationService, authz *Authorizer) *ConfigItemHandler {
	return &ConfigItemHandler{items: items, authz: authz}
}

// ConfigItemResponse is an item as a listing has it: what its newest version
// says, and how many versions there are.
type ConfigItemResponse struct {
	ID                uint      `json:"id"`
	Number            string    `json:"number"`
	ClassID           uint      `json:"classId"`
	Name              string    `json:"name"`
	DeploymentStateID uint      `json:"deploymentStateId"`
	IncidentStateID   uint      `json:"incidentStateId"`
	VersionCount      int       `json:"versionCount"`
	CreatedAt         time.Time `json:"createdAt"`
	ChangedAt         time.Time `json:"changedAt"`
	CreatedBy         uint      `json:"createdBy"`
	ChangedBy         uint      `json:"changedBy"`
}

// ConfigItemVersionResponse is one version of an item. Its values travel by
// the keys of the definition it was written under, in the shape model.
// AttributeData gives them, which is also how they are kept.
type ConfigItemVersionResponse struct {
	ID                uint                `json:"id"`
	DefinitionID      uint                `json:"definitionId"`
	Name              string              `json:"name"`
	DeploymentStateID uint                `json:"deploymentStateId"`
	IncidentStateID   uint                `json:"incidentStateId"`
	Data              model.AttributeData `json:"data"`
	CreatedAt         time.Time           `json:"createdAt"`
	CreatedBy         uint                `json:"createdBy"`
}

// ConfigItemDetailResponse is an item with the version it stands at.
type ConfigItemDetailResponse struct {
	ConfigItemResponse

	Version ConfigItemVersionResponse `json:"version"`
}

// ConfigItemRequest makes an item, or writes the next version of one; the
// class is named only when an item is made.
type ConfigItemRequest struct {
	ClassID           uint                `json:"classId"`
	Name              string              `json:"name"`
	DeploymentStateID uint                `json:"deploymentStateId"`
	IncidentStateID   uint                `json:"incidentStateId"`
	Data              model.AttributeData `json:"data"`
}

func (r ConfigItemRequest) command() service.ConfigItemVersionCommand {
	return service.ConfigItemVersionCommand{
		Name: r.Name, DeploymentStateID: r.DeploymentStateID, IncidentStateID: r.IncidentStateID, Data: r.Data,
	}
}

func newConfigItemResponse(item *model.ConfigItem) ConfigItemResponse {
	return ConfigItemResponse{
		ID: item.ID, Number: item.Number, ClassID: item.ClassID, Name: item.Name,
		DeploymentStateID: item.DeploymentStateID, IncidentStateID: item.IncidentStateID,
		VersionCount: item.VersionCount, CreatedAt: item.CreatedAt, ChangedAt: item.ChangedAt,
		CreatedBy: item.CreatedBy, ChangedBy: item.ChangedBy,
	}
}

func newConfigItemVersionResponse(version *model.ConfigItemVersion) ConfigItemVersionResponse {
	data := version.Data
	if data == nil {
		data = model.AttributeData{}
	}

	return ConfigItemVersionResponse{
		ID: version.ID, DefinitionID: version.DefinitionID, Name: version.Name,
		DeploymentStateID: version.DeploymentStateID, IncidentStateID: version.IncidentStateID, Data: data,
		CreatedAt: version.CreatedAt, CreatedBy: version.CreatedBy,
	}
}

func newConfigItemDetailResponse(item *service.ConfigItemWithVersion) ConfigItemDetailResponse {
	return ConfigItemDetailResponse{
		ConfigItemResponse: newConfigItemResponse(item.Item),
		Version:            newConfigItemVersionResponse(item.Version),
	}
}

// List handles GET /api/v1/cmdb/items: a page of the items, narrowed by
// ?class= and ?q=, by name.
func (h *ConfigItemHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAssetRead); err != nil {
		return nil, err
	}

	class, err := queryUint(c, "class")
	if err != nil {
		return nil, toHTTPError(err)
	}

	limit, err := strictInt(c.Param("limit"), "limit")
	if err != nil {
		return nil, toHTTPError(err)
	}

	page, err := strictInt(c.Param("page"), "page")
	if err != nil {
		return nil, toHTTPError(err)
	}

	items, total, err := h.items.List(c, service.ConfigItemListFilter{
		ClassID: class, Search: c.Param("q"), Limit: limit, Page: page,
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	listed := listOf(items, newConfigItemResponse)
	listed.TotalCount = total

	return listed, nil
}

// Get handles GET /api/v1/cmdb/items/{id}: the item with the version it stands at.
func (h *ConfigItemHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAssetRead); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	item, err := h.items.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newConfigItemDetailResponse(item), nil
}

// Versions handles GET /api/v1/cmdb/items/{id}/versions, the newest first.
func (h *ConfigItemHandler) Versions(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAssetRead); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	versions, err := h.items.Versions(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(versions, newConfigItemVersionResponse), nil
}

// Create handles POST /api/v1/cmdb/items: an item of the class named, its first
// version written under the definition the class has now.
func (h *ConfigItemHandler) Create(c *gofr.Context) (any, error) {
	principal, err := h.authz.Require(c, model.PermAssetWrite)
	if err != nil {
		return nil, err
	}

	var req ConfigItemRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	item, err := h.items.Create(c, principal.User.ID, req.ClassID, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newConfigItemDetailResponse(item), nil
}

// AddVersion handles POST /api/v1/cmdb/items/{id}/versions: the item's next
// version. There is no PUT: a version is never changed.
func (h *ConfigItemHandler) AddVersion(c *gofr.Context) (any, error) {
	principal, err := h.authz.Require(c, model.PermAssetWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req ConfigItemRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	item, err := h.items.AddVersion(c, principal.User.ID, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newConfigItemDetailResponse(item), nil
}

// Delete handles DELETE /api/v1/cmdb/items/{id}: the item and every version of it.
func (h *ConfigItemHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAssetWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.items.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}
