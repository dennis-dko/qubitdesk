package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// QueueHandler serves the queues tickets are sorted to, under /queues.
//
// Reading takes tickets:read or queues:write, and what a reader is shown is what
// the service says they reach; changing takes queues:write.
type QueueHandler struct {
	queues service.QueueService
	authz  *Authorizer
}

// NewQueueHandler creates the queue handler.
func NewQueueHandler(queues service.QueueService, authz *Authorizer) *QueueHandler {
	return &QueueHandler{queues: queues, authz: authz}
}

// QueueResponse is a queue as the API hands it out.
type QueueResponse struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	ParentID      *uint  `json:"parentId"`
	Description   string `json:"description"`
	FollowUp      string `json:"followUp"`
	UnlockMinutes int    `json:"unlockMinutes"`
	Valid         bool   `json:"valid"`

	// ServiceLevelID is the service level a new ticket in the queue starts with.
	ServiceLevelID *uint `json:"serviceLevelId"`

	// Reach is how far the caller reaches into the queue: read or write.
	Reach string `json:"reach"`

	// Access is which roles reach the queue, as far as the caller is shown: all
	// of them to whoever holds queues:write, their own line to anybody else.
	Access []QueueAccessResponse `json:"access"`
}

// QueueAccessResponse is one role's access to a queue.
type QueueAccessResponse struct {
	RoleID uint   `json:"roleId"`
	Level  string `json:"level"`
}

// QueueRequest creates or changes a queue. On a change an absent field is left
// as it was; ClearParent moves the queue to the top, because a ParentID sent as
// null cannot be told apart from one that was not sent.
type QueueRequest struct {
	Name          *string               `json:"name"`
	ParentID      *uint                 `json:"parentId"`
	ClearParent   bool                  `json:"clearParent"`
	Description   *string               `json:"description"`
	FollowUp      *string               `json:"followUp"`
	UnlockMinutes *int                  `json:"unlockMinutes"`
	Valid         *bool                 `json:"valid"`
	Access        *[]QueueAccessRequest `json:"access"`

	ServiceLevelID    *uint `json:"serviceLevelId"`
	ClearServiceLevel bool  `json:"clearServiceLevel"`
}

// QueueAccessRequest gives one role a level of access to a queue.
type QueueAccessRequest struct {
	RoleID uint   `json:"roleId"`
	Level  string `json:"level"`
}

func newQueueResponse(caller *service.Principal, queue *model.Queue) QueueResponse {
	reach, _ := service.Reach(caller, queue)

	access := make([]QueueAccessResponse, 0, len(queue.Access))
	for _, entry := range queue.Access {
		access = append(access, QueueAccessResponse{RoleID: entry.RoleID, Level: string(entry.Level)})
	}

	return QueueResponse{
		ID: queue.ID, Name: queue.Name, ParentID: queue.ParentID, Description: queue.Description,
		FollowUp: string(queue.FollowUp), UnlockMinutes: queue.UnlockMinutes, Valid: queue.Valid,
		ServiceLevelID: queue.ServiceLevelID, Reach: string(reach), Access: access,
	}
}

func (r QueueRequest) command() command.QueueCommand {
	cmd := command.QueueCommand{
		Name: r.Name, ParentID: r.ParentID, ClearParent: r.ClearParent, Description: r.Description,
		FollowUp: r.FollowUp, UnlockMinutes: r.UnlockMinutes, Valid: r.Valid,
		ServiceLevelID: r.ServiceLevelID, ClearServiceLevel: r.ClearServiceLevel,
	}

	if r.Access != nil {
		access := make([]command.QueueAccessCommand, 0, len(*r.Access))
		for _, entry := range *r.Access {
			access = append(access, command.QueueAccessCommand{RoleID: entry.RoleID, Level: entry.Level})
		}

		cmd.Access = &access
	}

	return cmd
}

// List handles GET /api/v1/queues.
func (h *QueueHandler) List(c *gofr.Context) (any, error) {
	caller, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite)
	if err != nil {
		return nil, err
	}

	queues, err := h.queues.ListQueues(c, caller)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(queues, func(queue *model.Queue) QueueResponse { return newQueueResponse(caller, queue) }), nil
}

// Get handles GET /api/v1/queues/{id}.
func (h *QueueHandler) Get(c *gofr.Context) (any, error) {
	caller, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	queue, err := h.queues.GetQueue(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newQueueResponse(caller, queue), nil
}

// Create handles POST /api/v1/queues.
func (h *QueueHandler) Create(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermQueueWrite)
	if err != nil {
		return nil, err
	}

	var req QueueRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	queue, err := h.queues.CreateQueue(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newQueueResponse(caller, queue), nil
}

// Update handles PUT /api/v1/queues/{id}.
func (h *QueueHandler) Update(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermQueueWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req QueueRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	queue, err := h.queues.UpdateQueue(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newQueueResponse(caller, queue), nil
}

// Delete handles DELETE /api/v1/queues/{id}.
func (h *QueueHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.queues.DeleteQueue(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}
