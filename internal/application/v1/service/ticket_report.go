package service

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// ReportDimension is what the rows of a report are the values of.
type ReportDimension string

// What a report's rows can be by, as OTRS's ticket statistics and every help
// desk's reports offer them: where the tickets were, how urgent, of what kind,
// whose, for whom, and under what promise.
const (
	ReportByQueue        ReportDimension = "queue"
	ReportByPriority     ReportDimension = "priority"
	ReportByType         ReportDimension = "type"
	ReportByOwner        ReportDimension = "owner"
	ReportByOrganisation ReportDimension = "organisation"
	ReportByServiceLevel ReportDimension = "serviceLevel"
)

// MaxReportDays bounds the span of one report: a year and a day, so that a
// whole year can be compared with nothing cut off its end.
const MaxReportDays = 366

// MaxReportTickets bounds the tickets one report reads. Chosen rather than
// derived: a report counts in memory, and an installation opening more than
// this in a span asks for a shorter one rather than for figures counted from
// part of its tickets.
const MaxReportTickets = 50000

// ReportDefaultDays is the span a report covers when none is given: the last
// thirty days, today included.
const ReportDefaultDays = 30

// ReportQuery is what a report is asked for: calendar days read on the caller's
// own calendar, both ends included, and what its rows are by.
type ReportQuery struct {
	Days Days
	By   ReportDimension
}

// TicketReport is what the statistics answer for a span of days, counted in the
// queues the caller reaches: a row for each value of the dimension asked for
// that any ticket of the span carries, the whole of the span, and the tickets
// opened and finished on each of its days.
type TicketReport struct {
	First time.Time
	Last  time.Time
	By    ReportDimension
	Rows  []ReportRow
	Total ReportRow
	Days  []DayLoad
}

// ReportRow is one value's tickets in a span. ID is 0, and Name empty, for the
// tickets that name no value - no owner, no organisation, no service level.
//
// What a promise owed counts the tickets it can be judged for: a first answer
// given, or run out ungiven, for a ticket opened in the span; a solution, for a
// ticket finished in it. A promise still running is owed nothing yet. The
// minutes are wall-clock minutes, summed, so that the average is the sum over
// what was counted - the first answers given, and the tickets finished.
type ReportRow struct {
	ID                uint
	Name              string
	Opened            uint
	Closed            uint
	ResponseOwed      uint
	ResponseKept      uint
	SolutionOwed      uint
	SolutionKept      uint
	Answered          uint
	ResponseMinutes   uint64
	ResolutionMinutes uint64
}

// Report counts the tickets of a span for the statistics.
func (s *TicketApplicationService) Report(
	ctx context.Context,
	caller *Principal,
	query ReportQuery,
) (*TicketReport, error) {
	if query.By == "" {
		query.By = ReportByQueue
	}

	if !slices.Contains([]ReportDimension{ReportByQueue, ReportByPriority, ReportByType, ReportByOwner,
		ReportByOrganisation, ReportByServiceLevel}, query.By) {
		return nil, apperror.InvalidFields("by")
	}

	zone := s.readerZone(ctx, caller)
	now := s.now().UTC()
	first, last, err := reportSpan(query.Days, now.In(zone))
	if err != nil {
		return nil, err
	}

	queues, err := s.queues.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	var reached []uint

	for _, queue := range queues {
		if _, ok := Reach(caller, queue); ok {
			reached = append(reached, queue.ID)
		}
	}

	span := spanOf(Days{First: &first, Last: &last}, zone)

	opened, err := s.reportTickets(ctx, repository.TicketFilter{QueueIDs: reached, Created: span})
	if err != nil {
		return nil, err
	}

	closed, err := s.reportTickets(ctx, repository.TicketFilter{QueueIDs: reached, Closed: span})
	if err != nil {
		return nil, err
	}

	report := &TicketReport{First: first, Last: last, By: query.By, Days: reportDays(first, last)}
	rows := map[uint]*ReportRow{}

	row := func(ticket *model.Ticket) *ReportRow {
		key := reportKey(ticket, query.By)
		if rows[key] == nil {
			rows[key] = &ReportRow{ID: key}
		}

		return rows[key]
	}

	for _, ticket := range opened {
		countOpened(row(ticket), ticket, now)
		countOpened(&report.Total, ticket, now)
		addToDay(report.Days, ticket.CreatedAt, zone, func(day *DayLoad) { day.Opened++ })
	}

	for _, ticket := range closed {
		countClosed(row(ticket), ticket)
		countClosed(&report.Total, ticket)
		addToDay(report.Days, *ticket.ClosedAt, zone, func(day *DayLoad) { day.Closed++ })
	}

	if err := s.nameReportRows(ctx, query.By, rows, queues); err != nil {
		return nil, err
	}

	for _, counted := range rows {
		report.Rows = append(report.Rows, *counted)
	}

	slices.SortFunc(report.Rows, func(a, b ReportRow) int {
		if c := cmp.Compare(b.Opened+b.Closed, a.Opened+a.Closed); c != 0 {
			return c
		}

		return cmp.Compare(a.Name, b.Name)
	})

	return report, nil
}

// reportSpan is the calendar days a report covers - the last thirty days where
// none are given, a span running to today where only its start is - each
// midnight UTC of its day, refused where it runs backwards or longer than a
// report may.
func reportSpan(days Days, today time.Time) (time.Time, time.Time, error) {
	last := model.CalendarDay(today)
	if days.Last != nil {
		last = model.CalendarDay(*days.Last)
	}

	first := last.AddDate(0, 0, 1-ReportDefaultDays)
	if days.First != nil {
		first = model.CalendarDay(*days.First)
	}

	if last.Before(first) {
		return first, last, apperror.InvalidFields("to")
	}

	if int(last.Sub(first).Hours()/24)+1 > MaxReportDays {
		return first, last, apperror.InvalidFields("from")
	}

	return first, last, nil
}

// reportTickets reads every ticket a filter allows, refusing a span holding
// more than a report counts rather than counting part of it.
func (s *TicketApplicationService) reportTickets(
	ctx context.Context,
	filter repository.TicketFilter,
) ([]*model.Ticket, error) {
	filter.Limit = MaxReportTickets + 1

	tickets, total, err := s.tickets.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	if total > MaxReportTickets {
		return nil, apperror.Invalidf("the span holds more than %d tickets", MaxReportTickets).
			WithCode("reportTooLarge", MaxReportTickets)
	}

	return tickets, nil
}

// reportKey is the value of the dimension a ticket is counted under; 0 is none.
func reportKey(ticket *model.Ticket, by ReportDimension) uint {
	optional := func(id *uint) uint {
		if id == nil {
			return 0
		}

		return *id
	}

	switch by {
	case ReportByPriority:
		return ticket.PriorityID
	case ReportByType:
		return ticket.TypeID
	case ReportByOwner:
		return optional(ticket.OwnerID)
	case ReportByOrganisation:
		return optional(ticket.OrganisationID)
	case ReportByServiceLevel:
		return optional(ticket.ServiceLevelID)
	default:
		return ticket.QueueID
	}
}

// countOpened counts a ticket opened in the span, and what it was owed for its
// first answer where that can be judged.
func countOpened(row *ReportRow, ticket *model.Ticket, now time.Time) {
	row.Opened++

	if ticket.FirstResponseAt != nil {
		row.Answered++
		row.ResponseMinutes += minutesBetween(ticket.CreatedAt, *ticket.FirstResponseAt)
	}

	switch due := ticket.FirstResponseDue; {
	case due == nil:
	case ticket.FirstResponseAt != nil:
		row.ResponseOwed++

		if !ticket.FirstResponseAt.After(*due) {
			row.ResponseKept++
		}
	case now.After(*due):
		row.ResponseOwed++
	}
}

// countClosed counts a ticket finished in the span, how long it took, and
// whether it was finished by the moment its solution was owed.
func countClosed(row *ReportRow, ticket *model.Ticket) {
	row.Closed++
	row.ResolutionMinutes += minutesBetween(ticket.CreatedAt, *ticket.ClosedAt)

	if due := ticket.SolutionDue; due != nil {
		row.SolutionOwed++

		if !ticket.ClosedAt.After(*due) {
			row.SolutionKept++
		}
	}
}

// minutesBetween is the whole minutes from one moment to a later one; none where
// the later one is not later.
func minutesBetween(from, to time.Time) uint64 {
	if !to.After(from) {
		return 0
	}

	return uint64(to.Sub(from) / time.Minute)
}

// reportDays is a DayLoad for every day of the span, in order.
func reportDays(first, last time.Time) []DayLoad {
	days := make([]DayLoad, 0, int(last.Sub(first).Hours()/24)+1)
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		days = append(days, DayLoad{Day: day})
	}

	return days
}

// addToDay counts a moment on the day of the span it fell on in zone.
func addToDay(days []DayLoad, at time.Time, zone *time.Location, add func(*DayLoad)) {
	if len(days) == 0 {
		return
	}

	index := int(model.CalendarDay(at.In(zone)).Sub(days[0].Day).Hours() / 24)
	if index >= 0 && index < len(days) {
		add(&days[index])
	}
}

// nameReportRows gives each row the name its value has now. A value gone since
// keeps its id and no name, as an erased account's articles name it by number.
func (s *TicketApplicationService) nameReportRows(
	ctx context.Context,
	by ReportDimension,
	rows map[uint]*ReportRow,
	queues []*model.Queue,
) error {
	names := map[uint]string{}

	switch by {
	case ReportByQueue:
		for _, queue := range queues {
			names[queue.ID] = queue.Name
		}
	case ReportByPriority:
		priorities, err := s.priorities.GetAll(ctx)
		if err != nil {
			return err
		}

		for _, priority := range priorities {
			names[priority.ID] = priority.Name
		}
	case ReportByType:
		types, err := s.types.GetAll(ctx)
		if err != nil {
			return err
		}

		for _, kind := range types {
			names[kind.ID] = kind.Name
		}
	case ReportByOwner:
		ids := make([]uint, 0, len(rows))
		for id := range rows {
			ids = append(ids, id)
		}

		people, err := s.Names(ctx, ids)
		if err != nil {
			return err
		}

		names = people
	case ReportByOrganisation:
		for id := range rows {
			if id == 0 {
				continue
			}

			organisation, err := s.organisations.GetByID(ctx, id)
			if apperror.KindOf(err) == apperror.KindNotFound {
				continue
			}

			if err != nil {
				return err
			}

			names[id] = organisation.Name
		}
	case ReportByServiceLevel:
		if s.levels == nil {
			break
		}

		levels, err := s.levels.GetAll(ctx)
		if err != nil {
			return err
		}

		for _, level := range levels {
			names[level.ID] = level.Name
		}
	}

	for id, row := range rows {
		row.Name = names[id]
	}

	return nil
}
