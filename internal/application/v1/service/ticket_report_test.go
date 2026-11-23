package service_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A report counts, in the queues the caller reaches, the tickets a span opened
// and finished, the first answers owed - given in time, given late, run out
// ungiven - the solutions owed, and the average minutes to the first answer and
// to the finish. Three tickets are opened at nine under a level promising an
// answer within the hour and a solution within eight: one answered after half an
// hour and finished after two, one answered after an hour and a half, one never
// answered; the report is read at noon.
func TestAReportCountsWhatASpanOpenedAndFinished(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	nine := time.Date(2026, time.November, 20, 9, 0, 0, 0, time.UTC)
	at := func(minutes int) {
		f.tickets.WithClock(func() time.Time { return nine.Add(time.Duration(minutes) * time.Minute) })
	}

	gold, err := f.levels.Save(ctx, &model.ServiceLevel{
		Name: "Gold", FirstResponseMinutes: 60, SolutionMinutes: 480, Valid: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	f.desk.ServiceLevelID = &gold.ID
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	at(0)
	quick := f.open(t, f.agent, f.desk, "Answered in time")
	late := f.open(t, f.agent, f.desk, "Answered late")
	f.open(t, f.agent, f.desk, "Never answered")
	f.open(t, f.admin, f.hidden, "In a queue the agent does not reach")

	answer := func(minutes int, ticket *model.Ticket) {
		at(minutes)
		if _, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{
			Body: ptr("On it."), CustomerVisible: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	answer(30, quick)
	answer(90, late)

	at(120)
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, quick.ID, command.UpdateTicketCommand{
		StateID: &f.states["closed"].ID,
	}); err != nil {
		t.Fatal(err)
	}

	at(180)
	day := model.CalendarDay(nine)

	report, err := f.tickets.Report(ctx, f.agent, service.ReportQuery{Days: service.Days{First: &day, Last: &day}})
	if err != nil {
		t.Fatal(err)
	}

	want := service.ReportRow{
		Opened: 3, Closed: 1, ResponseOwed: 3, ResponseKept: 1, SolutionOwed: 1, SolutionKept: 1,
		Answered: 2, ResponseMinutes: 30 + 90, ResolutionMinutes: 120,
	}

	if report.Total != want {
		t.Errorf("the span counts %+v, want %+v", report.Total, want)
	}

	desk := want
	desk.ID, desk.Name = f.desk.ID, f.desk.Name

	if len(report.Rows) != 1 || report.Rows[0] != desk {
		t.Errorf("by queue the span counts %+v, want only the desk's row %+v", report.Rows, desk)
	}

	if len(report.Days) != 1 || report.Days[0].Opened != 3 || report.Days[0].Closed != 1 {
		t.Errorf("the span's one day counts %+v, want three opened and one finished", report.Days)
	}

	byOwner, err := f.tickets.Report(ctx, f.agent, service.ReportQuery{
		Days: service.Days{First: &day, Last: &day}, By: service.ReportByOwner,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(byOwner.Rows) != 1 || byOwner.Rows[0].ID != 0 || byOwner.Rows[0].Name != "" || byOwner.Rows[0].Opened != 3 {
		t.Errorf("by owner the span counts %+v, want one row for the tickets nobody owns", byOwner.Rows)
	}
}

// A report covers the last thirty days where no span is given, today last, and
// refuses what it cannot count: a span running backwards, one longer than a
// report covers, and rows by something it does not count by.
func TestAReportsSpanIsTheLastThirtyDaysUnlessItIsGivenOne(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	now := time.Date(2026, time.November, 20, 9, 0, 0, 0, time.UTC)
	f.tickets.WithClock(func() time.Time { return now })

	report, err := f.tickets.Report(ctx, f.agent, service.ReportQuery{})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Days) != service.ReportDefaultDays || !report.Last.Equal(model.CalendarDay(now)) ||
		!report.First.Equal(model.CalendarDay(now).AddDate(0, 0, 1-service.ReportDefaultDays)) {
		t.Errorf("without a span the report covers %v to %v in %d days, want the thirty days up to today",
			report.First, report.Last, len(report.Days))
	}

	day := func(d int) *time.Time {
		at := model.CalendarDay(now).AddDate(0, 0, d)
		return &at
	}

	for field, query := range map[string]service.ReportQuery{
		"to":   {Days: service.Days{First: day(0), Last: day(-1)}},
		"from": {Days: service.Days{First: day(-service.MaxReportDays), Last: day(0)}},
		"by":   {By: "size"},
	} {
		_, err := f.tickets.Report(ctx, f.agent, query)
		if detail, ok := apperror.Detail(err); !ok || detail.Kind != apperror.KindInvalid ||
			!slices.Equal(detail.Fields, []string{field}) {
			t.Errorf("a report with a wrong %s answered %v, want it refused", field, err)
		}
	}

	if _, err := f.tickets.Report(ctx, f.agent, service.ReportQuery{
		Days: service.Days{First: day(1 - service.MaxReportDays), Last: day(0)},
	}); err != nil {
		t.Errorf("a report over %d days was refused: %v", service.MaxReportDays, err)
	}
}
