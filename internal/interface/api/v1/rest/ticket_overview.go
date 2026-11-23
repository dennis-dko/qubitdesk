package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// TicketOverviewResponse is an agent's overview as the API hands it out.
type TicketOverviewResponse struct {
	Owned       uint                `json:"owned"`
	Responsible uint                `json:"responsible"`
	Watched     uint                `json:"watched"`
	Overdue     uint                `json:"overdue"`
	Queues      []QueueLoadResponse `json:"queues"`
	Days        []DayLoadResponse   `json:"days"`
}

// QueueLoadResponse is one queue's unfinished tickets by where they stand.
type QueueLoadResponse struct {
	QueueID uint `json:"queueId"`
	New     uint `json:"new"`
	Open    uint `json:"open"`
	Waiting uint `json:"waiting"`
	Overdue uint `json:"overdue"`
}

// DayLoadResponse is the tickets opened and finished on one calendar day.
type DayLoadResponse struct {
	Day    Date `json:"day"`
	Opened uint `json:"opened"`
	Closed uint `json:"closed"`
}

// Overview handles GET /api/v1/tickets/overview: what an agent's overview
// shows them, in the queues they reach.
func (h *TicketHandler) Overview(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	overview, err := h.tickets.Overview(c, caller)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketOverviewResponse(overview), nil
}

func newTicketOverviewResponse(overview *service.TicketOverview) TicketOverviewResponse {
	response := TicketOverviewResponse{
		Owned: overview.Owned, Responsible: overview.Responsible, Watched: overview.Watched,
		Overdue: overview.Overdue, Queues: make([]QueueLoadResponse, 0, len(overview.Queues)),
		Days: make([]DayLoadResponse, 0, len(overview.Days)),
	}

	for _, load := range overview.Queues {
		response.Queues = append(response.Queues, QueueLoadResponse{
			QueueID: load.QueueID, New: load.New, Open: load.Open, Waiting: load.Waiting, Overdue: load.Overdue,
		})
	}

	for _, day := range overview.Days {
		response.Days = append(response.Days, DayLoadResponse{Day: Date{day.Day}, Opened: day.Opened, Closed: day.Closed})
	}

	return response
}
