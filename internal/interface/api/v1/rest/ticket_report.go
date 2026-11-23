package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// TicketReportResponse is a report as the API hands it out.
type TicketReportResponse struct {
	From  Date                `json:"from"`
	To    Date                `json:"to"`
	By    string              `json:"by"`
	Rows  []ReportRowResponse `json:"rows"`
	Total ReportRowResponse   `json:"total"`
	Days  []DayLoadResponse   `json:"days"`
}

// ReportRowResponse is one value's tickets in the span. The averages are whole
// wall-clock minutes, and absent where nothing was counted to average.
type ReportRowResponse struct {
	ID                       uint    `json:"id"`
	Name                     string  `json:"name"`
	Opened                   uint    `json:"opened"`
	Closed                   uint    `json:"closed"`
	ResponseOwed             uint    `json:"responseOwed"`
	ResponseKept             uint    `json:"responseKept"`
	SolutionOwed             uint    `json:"solutionOwed"`
	SolutionKept             uint    `json:"solutionKept"`
	AverageResponseMinutes   *uint64 `json:"averageResponseMinutes"`
	AverageResolutionMinutes *uint64 `json:"averageResolutionMinutes"`
}

// Report handles GET /api/v1/reports/tickets: the statistics of a span of days,
// from and to, by what by names.
func (h *TicketHandler) Report(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermReportRead)
	if err != nil {
		return nil, err
	}

	var query service.ReportQuery

	if query.Days.First, err = queryDay(c, "from"); err != nil {
		return nil, toHTTPError(err)
	}

	if query.Days.Last, err = queryDay(c, "to"); err != nil {
		return nil, toHTTPError(err)
	}

	switch by := c.Param("by"); by {
	case "", "queue":
		query.By = service.ReportByQueue
	case "priority":
		query.By = service.ReportByPriority
	case "type":
		query.By = service.ReportByType
	case "owner":
		query.By = service.ReportByOwner
	case "organisation":
		query.By = service.ReportByOrganisation
	case "serviceLevel":
		query.By = service.ReportByServiceLevel
	default:
		return nil, toHTTPError(apperror.InvalidFields("by"))
	}

	report, err := h.tickets.Report(c, caller, query)
	if err != nil {
		return nil, toHTTPError(err)
	}

	response := TicketReportResponse{
		From: Date{report.First}, To: Date{report.Last}, By: string(report.By),
		Rows: make([]ReportRowResponse, 0, len(report.Rows)), Total: newReportRowResponse(report.Total),
		Days: make([]DayLoadResponse, 0, len(report.Days)),
	}

	for _, row := range report.Rows {
		response.Rows = append(response.Rows, newReportRowResponse(row))
	}

	for _, day := range report.Days {
		response.Days = append(response.Days, DayLoadResponse{Day: Date{day.Day}, Opened: day.Opened, Closed: day.Closed})
	}

	return response, nil
}

func newReportRowResponse(row service.ReportRow) ReportRowResponse {
	average := func(sum uint64, count uint) *uint64 {
		if count == 0 {
			return nil
		}

		value := sum / uint64(count)

		return &value
	}

	return ReportRowResponse{
		ID: row.ID, Name: row.Name, Opened: row.Opened, Closed: row.Closed,
		ResponseOwed: row.ResponseOwed, ResponseKept: row.ResponseKept,
		SolutionOwed: row.SolutionOwed, SolutionKept: row.SolutionKept,
		AverageResponseMinutes:   average(row.ResponseMinutes, row.Answered),
		AverageResolutionMinutes: average(row.ResolutionMinutes, row.Closed),
	}
}
