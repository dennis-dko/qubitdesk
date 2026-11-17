package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// JobHandler serves the jobs, read and administered by whoever sets up the
// automation.
type JobHandler struct {
	jobs  *service.JobApplicationService
	authz *Authorizer
}

// NewJobHandler wires the handler.
func NewJobHandler(jobs *service.JobApplicationService, authz *Authorizer) *JobHandler {
	return &JobHandler{jobs: jobs, authz: authz}
}

// JobCriteriaBody is what a ticket has to be for a job to take it: in one of
// the queues, states, priorities and types named - a list naming none takes
// every one - and locked or not where locked says so.
type JobCriteriaBody struct {
	QueueIDs    []uint `json:"queueIds"`
	StateIDs    []uint `json:"stateIds"`
	PriorityIDs []uint `json:"priorityIds"`
	TypeIDs     []uint `json:"typeIds"`
	Locked      *bool  `json:"locked"`
}

// JobResponse is a job: its criteria, the macros it runs in order, the plans
// that say when, and when it last ran - null for never.
type JobResponse struct {
	ID          uint            `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Valid       bool            `json:"valid"`
	Criteria    JobCriteriaBody `json:"criteria"`
	MacroIDs    []uint          `json:"macroIds"`
	PlanIDs     []uint          `json:"planIds"`
	LastRunAt   *time.Time      `json:"lastRunAt"`
}

// JobRequest creates or changes a job; on a change an absent field stays as it
// was, and its criteria, macros and plans, where sent, take the place of what
// the job had.
type JobRequest struct {
	Name        *string          `json:"name"`
	Description *string          `json:"description"`
	Valid       *bool            `json:"valid"`
	Criteria    *JobCriteriaBody `json:"criteria"`
	MacroIDs    *[]uint          `json:"macroIds"`
	PlanIDs     *[]uint          `json:"planIds"`
}

// nonNil is a list as the API hands it out: empty rather than null.
func nonNil(ids []uint) []uint {
	if ids == nil {
		return []uint{}
	}

	return ids
}

func newJobResponse(job *model.Job) JobResponse {
	return JobResponse{
		ID: job.ID, Name: job.Name, Description: job.Description, Valid: job.Valid,
		Criteria: JobCriteriaBody{
			QueueIDs: nonNil(job.Criteria.QueueIDs), StateIDs: nonNil(job.Criteria.StateIDs),
			PriorityIDs: nonNil(job.Criteria.PriorityIDs), TypeIDs: nonNil(job.Criteria.TypeIDs),
			Locked: job.Criteria.Locked,
		},
		MacroIDs: nonNil(job.MacroIDs), PlanIDs: nonNil(job.PlanIDs), LastRunAt: job.LastRunAt,
	}
}

func (r JobRequest) command() command.JobCommand {
	cmd := command.JobCommand{
		Name: r.Name, Description: r.Description, Valid: r.Valid, MacroIDs: r.MacroIDs, PlanIDs: r.PlanIDs,
	}

	if r.Criteria != nil {
		cmd.Criteria = &command.JobCriteriaCommand{
			QueueIDs: r.Criteria.QueueIDs, StateIDs: r.Criteria.StateIDs, PriorityIDs: r.Criteria.PriorityIDs,
			TypeIDs: r.Criteria.TypeIDs, Locked: r.Criteria.Locked,
		}
	}

	return cmd
}

// List handles GET /api/v1/jobs.
func (h *JobHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	jobs, err := h.jobs.List(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(jobs, newJobResponse), nil
}

// Get handles GET /api/v1/jobs/{id}.
func (h *JobHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	job, err := h.jobs.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newJobResponse(job), nil
}

// Create handles POST /api/v1/jobs.
func (h *JobHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	var req JobRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	job, err := h.jobs.Create(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newJobResponse(job), nil
}

// Update handles PUT /api/v1/jobs/{id}.
func (h *JobHandler) Update(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req JobRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	job, err := h.jobs.Update(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newJobResponse(job), nil
}

// Delete handles DELETE /api/v1/jobs/{id}.
func (h *JobHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.jobs.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}
