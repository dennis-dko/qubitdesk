package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// SysConfigHandler serves the system configuration.
type SysConfigHandler struct {
	config *service.SysConfigService
	authz  *Authorizer
}

// NewSysConfigHandler wires the handler.
func NewSysConfigHandler(config *service.SysConfigService, authz *Authorizer) *SysConfigHandler {
	return &SysConfigHandler{config: config, authz: authz}
}

// ConfigResponse is an option as the API hands it out, its value and default in
// the option's own type, so that a switch reads as true or false.
type ConfigResponse struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Access      string `json:"access"`
	Default     any    `json:"default"`
	Value       any    `json:"value"`
	Modified    bool   `json:"modified"`
	MaxLength   int    `json:"maxLength,omitempty"`
}

// ConfigRequest changes an option, with a value in the option's own type.
type ConfigRequest struct {
	Value any `json:"value"`
}

func newConfigResponse(value service.ConfigValue) ConfigResponse {
	option := value.Option

	return ConfigResponse{
		Name: option.Name, Group: option.Group, Description: option.Description,
		Type: string(option.Type), Access: string(option.Access),
		Default: option.Typed(option.Default), Value: option.Typed(value.Value), Modified: value.Modified,
		MaxLength: option.MaxLength,
	}
}

// List handles GET /api/v1/settings/sysconfig: every option for whoever may change the
// installation's settings, and for any other account signed in the external
// ones, which the screens show.
func (h *SysConfigHandler) List(c *gofr.Context) (any, error) {
	caller, err := h.authz.Principal(c)
	if err != nil {
		return nil, err
	}

	values, err := h.config.List(c, caller.Can(model.PermSettingsManage))
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listResponse[ConfigResponse]{Items: make([]ConfigResponse, 0, len(values))}
	for _, value := range values {
		page.Items = append(page.Items, newConfigResponse(value))
	}

	page.TotalCount = uint(len(page.Items))

	return page, nil
}

// Set handles PUT /api/v1/settings/sysconfig/{name}.
func (h *SysConfigHandler) Set(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermSettingsManage); err != nil {
		return nil, err
	}

	var req ConfigRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	value, err := h.config.Set(c, c.PathParam("name"), req.Value)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newConfigResponse(value), nil
}

// Reset handles DELETE /api/v1/settings/sysconfig/{name}, which puts the option
// back to its default.
func (h *SysConfigHandler) Reset(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermSettingsManage); err != nil {
		return nil, err
	}

	if _, err := h.config.Reset(c, c.PathParam("name")); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "reset"}, nil
}
