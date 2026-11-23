package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// ServiceLevelRepository keeps the service levels in service_levels.
type ServiceLevelRepository struct{ base }

// NewServiceLevelRepository creates the repository for the given dialect.
func NewServiceLevelRepository(db DB, dialect string) *ServiceLevelRepository {
	return &ServiceLevelRepository{base{db: db, dialect: dialect}}
}

var _ repository.ServiceLevelRepository = (*ServiceLevelRepository)(nil)

const serviceLevelColumns = "id, name, description, calendar_id, first_response_minutes, update_minutes, " +
	"solution_minutes, valid"

func scanServiceLevel(row interface{ Scan(dest ...any) error }) (*model.ServiceLevel, error) {
	var (
		level    model.ServiceLevel
		calendar sql.NullInt64
	)

	err := row.Scan(&level.ID, &level.Name, &level.Description, &calendar, &level.FirstResponseMinutes,
		&level.UpdateMinutes, &level.SolutionMinutes, &level.Valid)
	if err != nil {
		return nil, err
	}

	if calendar.Valid {
		id := uint(calendar.Int64)
		level.CalendarID = &id
	}

	return &level, nil
}

func (r *ServiceLevelRepository) GetAll(ctx context.Context) ([]*model.ServiceLevel, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+serviceLevelColumns+" FROM service_levels ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	levels := make([]*model.ServiceLevel, 0)

	for rows.Next() {
		level, scanErr := scanServiceLevel(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		levels = append(levels, level)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return levels, nil
}

func (r *ServiceLevelRepository) GetByID(ctx context.Context, id uint) (*model.ServiceLevel, error) {
	level, err := scanServiceLevel(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+serviceLevelColumns+" FROM service_levels WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("sla", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return level, nil
}

func (r *ServiceLevelRepository) Save(ctx context.Context, level *model.ServiceLevel) (*model.ServiceLevel, error) {
	id, err := r.insert(ctx,
		"INSERT INTO service_levels (name, description, calendar_id, first_response_minutes, update_minutes, "+
			"solution_minutes, valid) VALUES (?, ?, ?, ?, ?, ?, ?)",
		level.Name, level.Description, level.CalendarID, level.FirstResponseMinutes, level.UpdateMinutes,
		level.SolutionMinutes, level.Valid)
	if err != nil {
		return nil, translateServiceLevelErr(err, level.Name)
	}

	return r.GetByID(ctx, id)
}

func (r *ServiceLevelRepository) Update(ctx context.Context, level *model.ServiceLevel) (*model.ServiceLevel, error) {
	found, err := r.update(ctx, "service_levels",
		"UPDATE service_levels SET name = ?, description = ?, calendar_id = ?, first_response_minutes = ?, "+
			"update_minutes = ?, solution_minutes = ?, valid = ? WHERE id = ?",
		level.ID,
		level.Name, level.Description, level.CalendarID, level.FirstResponseMinutes, level.UpdateMinutes,
		level.SolutionMinutes, level.Valid, level.ID)
	if err != nil {
		return nil, translateServiceLevelErr(err, level.Name)
	}

	if !found {
		return nil, apperror.NotFound("sla", strconv.FormatUint(uint64(level.ID), 10))
	}

	return r.GetByID(ctx, level.ID)
}

func (r *ServiceLevelRepository) Delete(ctx context.Context, id uint) error {
	affected, err := r.exec(ctx, "DELETE FROM service_levels WHERE id = ?", id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return apperror.NotFound("sla", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

func (r *ServiceLevelRepository) CountUses(ctx context.Context, id uint) (int, int, error) {
	var queues, tickets int

	if err := r.db.QueryRowContext(ctx, r.rebind("SELECT COUNT(*) FROM queues WHERE service_level_id = ?"), id).
		Scan(&queues); err != nil {
		return 0, 0, apperror.Internal(err)
	}

	if err := r.db.QueryRowContext(ctx, r.rebind("SELECT COUNT(*) FROM tickets WHERE service_level_id = ?"), id).
		Scan(&tickets); err != nil {
		return 0, 0, apperror.Internal(err)
	}

	return queues, tickets, nil
}

func translateServiceLevelErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a service level named %q already exists", name).
			WithCode("serviceLevelNameTaken", name)
	}

	return apperror.Internal(err)
}
