package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// CalendarRepository keeps the calendars in calendars, calendar_hours and
// calendar_free_days.
type CalendarRepository struct{ base }

// NewCalendarRepository creates the repository for the given dialect.
func NewCalendarRepository(db DB, dialect string) *CalendarRepository {
	return &CalendarRepository{base{db: db, dialect: dialect}}
}

var _ repository.CalendarRepository = (*CalendarRepository)(nil)

const calendarColumns = "id, name, description, zone, holiday_place, valid"

func scanCalendar(row interface{ Scan(dest ...any) error }) (*model.Calendar, error) {
	var calendar model.Calendar

	err := row.Scan(&calendar.ID, &calendar.Name, &calendar.Description, &calendar.Zone,
		&calendar.HolidayPlace, &calendar.Valid)
	if err != nil {
		return nil, err
	}

	return &calendar, nil
}

func (r *CalendarRepository) GetAll(ctx context.Context) ([]*model.Calendar, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+calendarColumns+" FROM calendars ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	calendars := make([]*model.Calendar, 0)

	for rows.Next() {
		calendar, scanErr := scanCalendar(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		calendars = append(calendars, calendar)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	for _, calendar := range calendars {
		if err := r.fill(ctx, calendar); err != nil {
			return nil, err
		}
	}

	return calendars, nil
}

func (r *CalendarRepository) GetByID(ctx context.Context, id uint) (*model.Calendar, error) {
	calendar, err := scanCalendar(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+calendarColumns+" FROM calendars WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("calendar", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	if err := r.fill(ctx, calendar); err != nil {
		return nil, err
	}

	return calendar, nil
}

func (r *CalendarRepository) Save(ctx context.Context, calendar *model.Calendar) (*model.Calendar, error) {
	var id uint

	err := r.withTx(ctx, func(tx base) error {
		newID, err := tx.insert(ctx,
			"INSERT INTO calendars (name, description, zone, holiday_place, valid) VALUES (?, ?, ?, ?, ?)",
			calendar.Name, calendar.Description, calendar.Zone, calendar.HolidayPlace, calendar.Valid)
		if err != nil {
			return translateCalendarErr(err, calendar.Name)
		}

		id = newID

		return replaceCalendarDays(ctx, tx, newID, calendar)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, id)
}

func (r *CalendarRepository) Update(ctx context.Context, calendar *model.Calendar) (*model.Calendar, error) {
	err := r.withTx(ctx, func(tx base) error {
		found, err := tx.update(ctx, "calendars",
			"UPDATE calendars SET name = ?, description = ?, zone = ?, holiday_place = ?, valid = ? WHERE id = ?",
			calendar.ID,
			calendar.Name, calendar.Description, calendar.Zone, calendar.HolidayPlace, calendar.Valid, calendar.ID)
		if err != nil {
			return translateCalendarErr(err, calendar.Name)
		}

		if !found {
			return apperror.NotFound("calendar", strconv.FormatUint(uint64(calendar.ID), 10))
		}

		return replaceCalendarDays(ctx, tx, calendar.ID, calendar)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, calendar.ID)
}

func (r *CalendarRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		for _, query := range []string{
			"DELETE FROM calendar_hours WHERE calendar_id = ?",
			"DELETE FROM calendar_free_days WHERE calendar_id = ?",
		} {
			if _, err := tx.exec(ctx, query, id); err != nil {
				return apperror.Internal(err)
			}
		}

		affected, err := tx.exec(ctx, "DELETE FROM calendars WHERE id = ?", id)
		if err != nil {
			return apperror.Internal(err)
		}

		if affected == 0 {
			return apperror.NotFound("calendar", strconv.FormatUint(uint64(id), 10))
		}

		return nil
	})
}

func (r *CalendarRepository) CountServiceLevels(ctx context.Context, id uint) (int, error) {
	var count int

	err := r.db.QueryRowContext(ctx, r.rebind("SELECT COUNT(*) FROM service_levels WHERE calendar_id = ?"), id).
		Scan(&count)
	if err != nil {
		return 0, apperror.Internal(err)
	}

	return count, nil
}

// fill reads a calendar's hours and free days, earliest first.
func (r *CalendarRepository) fill(ctx context.Context, calendar *model.Calendar) error {
	hours, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT weekday, from_minute, to_minute FROM calendar_hours WHERE calendar_id = ? "+
			"ORDER BY weekday, from_minute"), calendar.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	defer func() { _ = hours.Close() }()

	calendar.Hours = make([]model.WorkingHours, 0)

	for hours.Next() {
		var stretch model.WorkingHours

		var weekday int
		if err := hours.Scan(&weekday, &stretch.From, &stretch.To); err != nil {
			return apperror.Internal(err)
		}

		stretch.Weekday = time.Weekday(weekday)
		calendar.Hours = append(calendar.Hours, stretch)
	}

	if err := hours.Err(); err != nil {
		return apperror.Internal(err)
	}

	days, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT day, recurring, name FROM calendar_free_days WHERE calendar_id = ? ORDER BY day"), calendar.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	defer func() { _ = days.Close() }()

	calendar.FreeDays = make([]model.FreeDay, 0)

	for days.Next() {
		var free model.FreeDay
		if err := days.Scan(&free.Day, &free.Recurring, &free.Name); err != nil {
			return apperror.Internal(err)
		}

		free.Day = model.CalendarDay(free.Day.UTC())
		calendar.FreeDays = append(calendar.FreeDays, free)
	}

	if err := days.Err(); err != nil {
		return apperror.Internal(err)
	}

	return nil
}

// replaceCalendarDays writes a calendar's hours and free days in place of the
// ones it had.
func replaceCalendarDays(ctx context.Context, tx base, id uint, calendar *model.Calendar) error {
	for _, query := range []string{
		"DELETE FROM calendar_hours WHERE calendar_id = ?",
		"DELETE FROM calendar_free_days WHERE calendar_id = ?",
	} {
		if _, err := tx.exec(ctx, query, id); err != nil {
			return apperror.Internal(err)
		}
	}

	for _, stretch := range calendar.Hours {
		_, err := tx.exec(ctx,
			"INSERT INTO calendar_hours (calendar_id, weekday, from_minute, to_minute) VALUES (?, ?, ?, ?)",
			id, int(stretch.Weekday), stretch.From, stretch.To)
		if err != nil {
			return apperror.Internal(err)
		}
	}

	for _, free := range calendar.FreeDays {
		_, err := tx.exec(ctx,
			"INSERT INTO calendar_free_days (calendar_id, day, recurring, name) VALUES (?, ?, ?, ?)",
			id, model.CalendarDay(free.Day), free.Recurring, free.Name)
		if err != nil {
			return apperror.Internal(err)
		}
	}

	return nil
}

func translateCalendarErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a calendar named %q already exists", name).WithCode("calendarNameTaken", name)
	}

	return apperror.Internal(err)
}
