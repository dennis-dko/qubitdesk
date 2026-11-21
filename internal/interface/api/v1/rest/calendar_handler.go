package rest

import (
	"fmt"
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// CalendarHandler serves the calendars, which whoever sets up queues
// administers: a calendar is what a service level's minutes are counted in.
type CalendarHandler struct {
	calendars *service.CalendarApplicationService
	authz     *Authorizer
}

// NewCalendarHandler wires the handler.
func NewCalendarHandler(calendars *service.CalendarApplicationService, authz *Authorizer) *CalendarHandler {
	return &CalendarHandler{calendars: calendars, authz: authz}
}

// CalendarHoursDTO is one stretch of a weekday, its times as the wall reads them:
// weekday 0 is Sunday, and "24:00" ends a stretch at midnight.
type CalendarHoursDTO struct {
	Weekday int    `json:"weekday"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// CalendarFreeDayDTO is a free day, its date as 2026-12-24.
type CalendarFreeDayDTO struct {
	Day       string `json:"day"`
	Recurring bool   `json:"recurring"`
	Name      string `json:"name"`
}

// CalendarResponse is a calendar with its hours and free days. An empty zone or
// holidayPlace follows the installation's own; holidayPlace "none" keeps no
// public holidays at all.
type CalendarResponse struct {
	ID           uint                 `json:"id"`
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	Zone         string               `json:"zone"`
	HolidayPlace string               `json:"holidayPlace"`
	Hours        []CalendarHoursDTO   `json:"hours"`
	FreeDays     []CalendarFreeDayDTO `json:"freeDays"`
	Valid        bool                 `json:"valid"`
}

// CalendarListResponse is the calendars, with the countries and regions a
// calendar's public holidays can be kept in - what the form that makes one
// offers, for somebody who sets up queues without administering the
// installation, whose holiday card is the other place the list is shown.
type CalendarListResponse struct {
	Items            []CalendarResponse       `json:"items"`
	TotalCount       uint                     `json:"totalCount"`
	HolidayCountries []HolidayCountryResponse `json:"holidayCountries"`
}

// CalendarRequest creates or changes a calendar; on a change an absent field
// stays as it was, and hours or freeDays that are sent replace the old ones.
type CalendarRequest struct {
	Name         *string               `json:"name"`
	Description  *string               `json:"description"`
	Zone         *string               `json:"zone"`
	HolidayPlace *string               `json:"holidayPlace"`
	Hours        *[]CalendarHoursDTO   `json:"hours"`
	FreeDays     *[]CalendarFreeDayDTO `json:"freeDays"`
	Valid        *bool                 `json:"valid"`
}

func newCalendarResponse(calendar *model.Calendar) CalendarResponse {
	response := CalendarResponse{
		ID: calendar.ID, Name: calendar.Name, Description: calendar.Description, Zone: calendar.Zone,
		HolidayPlace: calendar.HolidayPlace, Valid: calendar.Valid,
		Hours: make([]CalendarHoursDTO, 0, len(calendar.Hours)), FreeDays: make([]CalendarFreeDayDTO, 0, len(calendar.FreeDays)),
	}

	for _, stretch := range calendar.Hours {
		response.Hours = append(response.Hours, CalendarHoursDTO{
			Weekday: int(stretch.Weekday), From: wallTime(stretch.From), To: wallTime(stretch.To),
		})
	}

	for _, free := range calendar.FreeDays {
		response.FreeDays = append(response.FreeDays, CalendarFreeDayDTO{
			Day: free.Day.Format(time.DateOnly), Recurring: free.Recurring, Name: free.Name,
		})
	}

	return response
}

// command reads the request's times and dates, which only the wire writes as
// text; a stretch or a day that cannot be read refuses its field.
func (r CalendarRequest) command() (command.CalendarCommand, error) {
	cmd := command.CalendarCommand{
		Name: r.Name, Description: r.Description, Zone: r.Zone, HolidayPlace: r.HolidayPlace, Valid: r.Valid,
	}

	if r.Hours != nil {
		hours := make([]model.WorkingHours, 0, len(*r.Hours))

		for _, sent := range *r.Hours {
			from, fromOK := minutesOf(sent.From)
			to, toOK := minutesOf(sent.To)

			if !fromOK || !toOK || sent.Weekday < 0 || sent.Weekday > 6 {
				return command.CalendarCommand{}, apperror.InvalidFields("hours")
			}

			hours = append(hours, model.WorkingHours{Weekday: time.Weekday(sent.Weekday), From: from, To: to})
		}

		cmd.Hours = &hours
	}

	if r.FreeDays != nil {
		days := make([]model.FreeDay, 0, len(*r.FreeDays))

		for _, sent := range *r.FreeDays {
			day, err := time.Parse(time.DateOnly, sent.Day)
			if err != nil {
				return command.CalendarCommand{}, apperror.InvalidFields("freeDays")
			}

			days = append(days, model.FreeDay{Day: day, Recurring: sent.Recurring, Name: sent.Name})
		}

		cmd.FreeDays = &days
	}

	return cmd, nil
}

// minutesOf reads "08:30" as minutes after midnight; "24:00" is midnight at the
// end of the day, which only a stretch's end can be - ValidHours says so.
func minutesOf(clock string) (int, bool) {
	var hour, minute int

	if n, err := fmt.Sscanf(clock, "%2d:%2d", &hour, &minute); err != nil || n != 2 || len(clock) != 5 {
		return 0, false
	}

	if minute < 0 || minute > 59 || hour < 0 || hour > 24 || (hour == 24 && minute != 0) {
		return 0, false
	}

	return hour*60 + minute, true
}

// wallTime writes minutes after midnight as the wall reads them.
func wallTime(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// List handles GET /api/v1/calendars.
func (h *CalendarHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	calendars, err := h.calendars.List(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listOf(calendars, newCalendarResponse)

	return CalendarListResponse{Items: page.Items, TotalCount: page.TotalCount, HolidayCountries: holidayCountries()}, nil
}

// Get handles GET /api/v1/calendars/{id}.
func (h *CalendarHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	calendar, err := h.calendars.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newCalendarResponse(calendar), nil
}

// Create handles POST /api/v1/calendars.
func (h *CalendarHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	var req CalendarRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	cmd, err := req.command()
	if err != nil {
		return nil, toHTTPError(err)
	}

	calendar, err := h.calendars.Create(c, cmd)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newCalendarResponse(calendar), nil
}

// Update handles PUT /api/v1/calendars/{id}.
func (h *CalendarHandler) Update(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req CalendarRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	cmd, err := req.command()
	if err != nil {
		return nil, toHTTPError(err)
	}

	calendar, err := h.calendars.Update(c, id, cmd)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newCalendarResponse(calendar), nil
}

// Delete handles DELETE /api/v1/calendars/{id}.
func (h *CalendarHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.calendars.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}
