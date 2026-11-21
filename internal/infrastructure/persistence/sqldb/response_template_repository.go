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

// ResponseTemplateRepository keeps the answer templates in response_templates.
type ResponseTemplateRepository struct{ base }

// NewResponseTemplateRepository creates the repository for the given dialect.
func NewResponseTemplateRepository(db DB, dialect string) *ResponseTemplateRepository {
	return &ResponseTemplateRepository{base{db: db, dialect: dialect}}
}

var _ repository.ResponseTemplateRepository = (*ResponseTemplateRepository)(nil)

const responseTemplateColumns = "id, name, subject, body, queue_id, valid"

func scanResponseTemplate(row interface{ Scan(dest ...any) error }) (*model.ResponseTemplate, error) {
	var (
		template model.ResponseTemplate
		queueID  sql.NullInt64
	)

	if err := row.Scan(&template.ID, &template.Name, &template.Subject, &template.Body, &queueID,
		&template.Valid); err != nil {
		return nil, err
	}

	if queueID.Valid {
		id := uint(queueID.Int64)
		template.QueueID = &id
	}

	return &template, nil
}

func (r *ResponseTemplateRepository) GetAll(ctx context.Context) ([]*model.ResponseTemplate, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+responseTemplateColumns+" FROM response_templates ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	templates := make([]*model.ResponseTemplate, 0)

	for rows.Next() {
		template, scanErr := scanResponseTemplate(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		templates = append(templates, template)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return templates, nil
}

func (r *ResponseTemplateRepository) GetByID(ctx context.Context, id uint) (*model.ResponseTemplate, error) {
	template, err := scanResponseTemplate(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+responseTemplateColumns+" FROM response_templates WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("template", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return template, nil
}

func (r *ResponseTemplateRepository) Save(ctx context.Context, template *model.ResponseTemplate) (*model.ResponseTemplate, error) {
	id, err := r.insert(ctx,
		"INSERT INTO response_templates (name, subject, body, queue_id, valid) VALUES (?, ?, ?, ?, ?)",
		template.Name, template.Subject, template.Body, template.QueueID, template.Valid)
	if err != nil {
		return nil, translateResponseTemplateErr(err, template.Name)
	}

	return r.GetByID(ctx, id)
}

func (r *ResponseTemplateRepository) Update(ctx context.Context, template *model.ResponseTemplate) (*model.ResponseTemplate, error) {
	found, err := r.update(ctx, "response_templates",
		"UPDATE response_templates SET name = ?, subject = ?, body = ?, queue_id = ?, valid = ? WHERE id = ?",
		template.ID, template.Name, template.Subject, template.Body, template.QueueID, template.Valid,
		template.ID)
	if err != nil {
		return nil, translateResponseTemplateErr(err, template.Name)
	}

	if !found {
		return nil, apperror.NotFound("template", strconv.FormatUint(uint64(template.ID), 10))
	}

	return r.GetByID(ctx, template.ID)
}

func (r *ResponseTemplateRepository) Delete(ctx context.Context, id uint) error {
	affected, err := r.exec(ctx, "DELETE FROM response_templates WHERE id = ?", id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return apperror.NotFound("template", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

func (r *ResponseTemplateRepository) CountForQueue(ctx context.Context, queueID uint) (int, error) {
	var count int

	if err := r.db.QueryRowContext(ctx, r.rebind("SELECT COUNT(*) FROM response_templates WHERE queue_id = ?"),
		queueID).Scan(&count); err != nil {
		return 0, apperror.Internal(err)
	}

	return count, nil
}

func translateResponseTemplateErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a template named %q already exists", name).WithCode("templateNameTaken", name)
	}

	return apperror.Internal(err)
}
