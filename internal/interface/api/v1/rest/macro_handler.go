package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// MacroHandler serves the macros, read and administered by whoever sets up the
// automation: a macro runs as the application on every ticket a job finds, so
// reading one says what any of them may have done.
type MacroHandler struct {
	macros *service.MacroApplicationService
	authz  *Authorizer
}

// NewMacroHandler wires the handler.
func NewMacroHandler(macros *service.MacroApplicationService, authz *Authorizer) *MacroHandler {
	return &MacroHandler{macros: macros, authz: authz}
}

// MacroResponse is a macro with its steps in the order they are taken.
type MacroResponse struct {
	ID          uint                  `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Valid       bool                  `json:"valid"`
	Actions     []MacroActionResponse `json:"actions"`
}

// MacroActionResponse is one step of a macro, its parameters by name.
type MacroActionResponse struct {
	Type       string            `json:"type"`
	Parameters map[string]string `json:"parameters"`
}

// MacroListResponse is the macros, and every step there is with what it reads,
// so that a form asks for exactly what the server will take.
type MacroListResponse struct {
	Items      []MacroResponse         `json:"items"`
	TotalCount uint                    `json:"totalCount"`
	Steps      []MacroStepTypeResponse `json:"steps"`
}

// MacroStepTypeResponse is a step a macro can take and the parameters it reads,
// in the order a form asks for them.
type MacroStepTypeResponse struct {
	Type       string                   `json:"type"`
	Parameters []MacroParameterResponse `json:"parameters"`
}

// MacroParameterResponse is one parameter of a step: what it holds, whether the
// step needs it, and the longest text or the most minutes it takes - zero where
// it holds neither.
type MacroParameterResponse struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Max      int    `json:"max"`
}

// MacroRequest creates or changes a macro; on a change an absent field stays as
// it was, and actions, where sent, take the place of every step it had.
type MacroRequest struct {
	Name        *string               `json:"name"`
	Description *string               `json:"description"`
	Valid       *bool                 `json:"valid"`
	Actions     *[]MacroActionRequest `json:"actions"`
}

// MacroActionRequest is one step as it is sent.
type MacroActionRequest struct {
	Type       string            `json:"type"`
	Parameters map[string]string `json:"parameters"`
}

func newMacroResponse(macro *model.Macro) MacroResponse {
	actions := make([]MacroActionResponse, 0, len(macro.Actions))
	for _, action := range macro.Actions {
		actions = append(actions, MacroActionResponse{Type: string(action.Type), Parameters: action.Parameters})
	}

	return MacroResponse{
		ID: macro.ID, Name: macro.Name, Description: macro.Description, Valid: macro.Valid, Actions: actions,
	}
}

func (r MacroRequest) command() command.MacroCommand {
	cmd := command.MacroCommand{Name: r.Name, Description: r.Description, Valid: r.Valid}

	if r.Actions != nil {
		actions := make([]command.MacroActionCommand, 0, len(*r.Actions))
		for _, action := range *r.Actions {
			actions = append(actions, command.MacroActionCommand{Type: action.Type, Parameters: action.Parameters})
		}

		cmd.Actions = &actions
	}

	return cmd
}

// macroSteps is every step there is, as the list answers it.
func macroSteps() []MacroStepTypeResponse {
	steps := make([]MacroStepTypeResponse, 0)

	for _, definition := range model.MacroActionDefinitions() {
		parameters := make([]MacroParameterResponse, 0, len(definition.Parameters))
		for _, parameter := range definition.Parameters {
			parameters = append(parameters, MacroParameterResponse{
				Name: parameter.Name, Kind: string(parameter.Kind), Required: parameter.Required, Max: parameter.Max,
			})
		}

		steps = append(steps, MacroStepTypeResponse{Type: string(definition.Type), Parameters: parameters})
	}

	return steps
}

// List handles GET /api/v1/macros.
func (h *MacroHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	macros, err := h.macros.List(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listOf(macros, newMacroResponse)

	return MacroListResponse{Items: page.Items, TotalCount: page.TotalCount, Steps: macroSteps()}, nil
}

// Get handles GET /api/v1/macros/{id}.
func (h *MacroHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	macro, err := h.macros.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newMacroResponse(macro), nil
}

// Create handles POST /api/v1/macros.
func (h *MacroHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	var req MacroRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	macro, err := h.macros.Create(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newMacroResponse(macro), nil
}

// Update handles PUT /api/v1/macros/{id}.
func (h *MacroHandler) Update(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req MacroRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	macro, err := h.macros.Update(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newMacroResponse(macro), nil
}

// Delete handles DELETE /api/v1/macros/{id}.
func (h *MacroHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.macros.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}
