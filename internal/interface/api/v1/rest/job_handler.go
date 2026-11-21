package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// JobHandler serves the jobs, read and administered by whoever sets up the
// automation.
type JobHandler struct {
	jobs       *service.JobApplicationService
	automation *service.AutomationService
	authz      *Authorizer
}

// NewJobHandler wires the handler.
func NewJobHandler(
	jobs *service.JobApplicationService,
	automation *service.AutomationService,
	authz *Authorizer,
) *JobHandler {
	return &JobHandler{jobs: jobs, automation: automation, authz: authz}
}

// JobRunResponse is what one run of a job did: how many tickets it found, on
// how many every step was taken and on how many one was refused, and its lines.
type JobRunResponse struct {
	ID         uint              `json:"id"`
	JobID      uint              `json:"jobId"`
	Trigger    string            `json:"trigger"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt time.Time         `json:"finishedAt"`
	Tickets    int               `json:"tickets"`
	Changed    int               `json:"changed"`
	Failed     int               `json:"failed"`
	Lines      []RunLineResponse `json:"lines"`
}

// RunLineResponse is one thing a run said: about which ticket, which macro and
// which of its steps, with the refusal's code and values as the API answers
// them and its English.
type RunLineResponse struct {
	TicketID *uint     `json:"ticketId"`
	MacroID  *uint     `json:"macroId"`
	Step     int       `json:"step"`
	Code     string    `json:"code"`
	Values   []any     `json:"values"`
	Message  string    `json:"message"`
	At       time.Time `json:"at"`
}

func newJobRunResponse(run *model.JobRun) JobRunResponse {
	lines := make([]RunLineResponse, 0, len(run.Lines))
	for _, line := range run.Lines {
		values := line.Values
		if values == nil {
			values = []any{}
		}

		lines = append(lines, RunLineResponse{
			TicketID: line.TicketID, MacroID: line.MacroID, Step: line.Step, Code: line.Code, Values: values,
			Message: line.Message, At: line.At,
		})
	}

	return JobRunResponse{
		ID: run.ID, JobID: run.JobID, Trigger: string(run.Trigger), StartedAt: run.StartedAt,
		FinishedAt: run.FinishedAt, Tickets: run.Tickets, Changed: run.Changed, Failed: run.Failed, Lines: lines,
	}
}

// Run handles POST /api/v1/jobs/{id}/run: the job runs once, now, and the
// answer is what it did.
func (h *JobHandler) Run(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	run, err := h.automation.Run(c, id, model.RunManual)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newJobRunResponse(run), nil
}

// Runs handles GET /api/v1/jobs/{id}/runs: the job's latest runs, the latest
// first.
func (h *JobHandler) Runs(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	runs, err := h.automation.Runs(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(runs, newJobRunResponse), nil
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
		Criteria: newJobCriteriaBody(job.Criteria),
		MacroIDs: nonNil(job.MacroIDs), PlanIDs: nonNil(job.PlanIDs), LastRunAt: job.LastRunAt,
	}
}

// newJobCriteriaBody is criteria as the API hands them out, a list naming
// nothing written as an empty list.
func newJobCriteriaBody(criteria model.TicketCriteria) JobCriteriaBody {
	return JobCriteriaBody{
		QueueIDs: nonNil(criteria.QueueIDs), StateIDs: nonNil(criteria.StateIDs),
		PriorityIDs: nonNil(criteria.PriorityIDs), TypeIDs: nonNil(criteria.TypeIDs), Locked: criteria.Locked,
	}
}

// command is criteria as they were sent.
func (b JobCriteriaBody) command() *command.JobCriteriaCommand {
	return &command.JobCriteriaCommand{
		QueueIDs: b.QueueIDs, StateIDs: b.StateIDs, PriorityIDs: b.PriorityIDs, TypeIDs: b.TypeIDs, Locked: b.Locked,
	}
}

func (r JobRequest) command() command.JobCommand {
	cmd := command.JobCommand{
		Name: r.Name, Description: r.Description, Valid: r.Valid, MacroIDs: r.MacroIDs, PlanIDs: r.PlanIDs,
	}

	if r.Criteria != nil {
		cmd.Criteria = r.Criteria.command()
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
