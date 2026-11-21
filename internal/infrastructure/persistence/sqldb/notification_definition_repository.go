package sqldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// NotificationDefinitionRepository keeps the notifications in
// notification_definitions and what each says, a message per language, in
// notification_messages.
type NotificationDefinitionRepository struct{ base }

// NewNotificationDefinitionRepository creates the repository for the given
// dialect.
func NewNotificationDefinitionRepository(db DB, dialect string) *NotificationDefinitionRepository {
	return &NotificationDefinitionRepository{base{db: db, dialect: dialect}}
}

var _ repository.NotificationDefinitionRepository = (*NotificationDefinitionRepository)(nil)

const notificationDefinitionColumns = "id, name, description, valid, data"

// notificationData is how a notification's events, tickets and recipients are
// kept in its data column, as KIX18 keeps them beside the notification.
type notificationData struct {
	Events     []string    `json:"events"`
	Criteria   jobCriteria `json:"criteria"`
	Recipients []string    `json:"recipients"`
	OncePerDay bool        `json:"oncePerDay,omitempty"`
	Optional   bool        `json:"optional,omitempty"`
}

func scanNotificationDefinition(row interface{ Scan(dest ...any) error }) (*model.NotificationDefinition, error) {
	var (
		definition model.NotificationDefinition
		data       string
	)

	if err := row.Scan(&definition.ID, &definition.Name, &definition.Description, &definition.Valid, &data); err != nil {
		return nil, err
	}

	var kept notificationData
	if err := json.Unmarshal([]byte(data), &kept); err != nil {
		return nil, err
	}

	definition.Events = make([]model.TicketEvent, 0, len(kept.Events))
	for _, event := range kept.Events {
		definition.Events = append(definition.Events, model.TicketEvent(event))
	}

	definition.Criteria = model.TicketCriteria{
		QueueIDs: kept.Criteria.QueueIDs, StateIDs: kept.Criteria.StateIDs, PriorityIDs: kept.Criteria.PriorityIDs,
		TypeIDs: kept.Criteria.TypeIDs, Locked: kept.Criteria.Locked,
	}

	definition.Recipients = make([]model.NotificationRecipient, 0, len(kept.Recipients))
	for _, recipient := range kept.Recipients {
		definition.Recipients = append(definition.Recipients, model.NotificationRecipient(recipient))
	}

	definition.OncePerDay, definition.Optional = kept.OncePerDay, kept.Optional

	return &definition, nil
}

// encodeNotificationData is a notification's data as it is kept.
func encodeNotificationData(definition *model.NotificationDefinition) (string, error) {
	kept := notificationData{
		Events: make([]string, 0, len(definition.Events)),
		Criteria: jobCriteria{
			QueueIDs: definition.Criteria.QueueIDs, StateIDs: definition.Criteria.StateIDs,
			PriorityIDs: definition.Criteria.PriorityIDs, TypeIDs: definition.Criteria.TypeIDs,
			Locked: definition.Criteria.Locked,
		},
		Recipients: make([]string, 0, len(definition.Recipients)),
		OncePerDay: definition.OncePerDay,
		Optional:   definition.Optional,
	}

	for _, event := range definition.Events {
		kept.Events = append(kept.Events, string(event))
	}

	for _, recipient := range definition.Recipients {
		kept.Recipients = append(kept.Recipients, string(recipient))
	}

	encoded, err := json.Marshal(kept)

	return string(encoded), err
}

func (r *NotificationDefinitionRepository) GetAll(ctx context.Context) ([]*model.NotificationDefinition, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+notificationDefinitionColumns+" FROM notification_definitions ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	definitions := make([]*model.NotificationDefinition, 0)

	for rows.Next() {
		definition, scanErr := scanNotificationDefinition(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		definitions = append(definitions, definition)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	for _, definition := range definitions {
		if err := r.fill(ctx, definition); err != nil {
			return nil, err
		}
	}

	return definitions, nil
}

func (r *NotificationDefinitionRepository) GetByID(ctx context.Context, id uint) (*model.NotificationDefinition, error) {
	definition, err := scanNotificationDefinition(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+notificationDefinitionColumns+" FROM notification_definitions WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("notification", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	if err := r.fill(ctx, definition); err != nil {
		return nil, err
	}

	return definition, nil
}

func (r *NotificationDefinitionRepository) Save(
	ctx context.Context,
	definition *model.NotificationDefinition,
) (*model.NotificationDefinition, error) {
	data, err := encodeNotificationData(definition)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	var id uint

	err = r.withTx(ctx, func(tx base) error {
		newID, err := tx.insert(ctx,
			"INSERT INTO notification_definitions (name, description, valid, data) VALUES (?, ?, ?, ?)",
			definition.Name, definition.Description, definition.Valid, data)
		if err != nil {
			return translateNotificationDefinitionErr(err, definition.Name)
		}

		id = newID

		return replaceNotificationMessages(ctx, tx, newID, definition.Messages)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, id)
}

func (r *NotificationDefinitionRepository) Update(
	ctx context.Context,
	definition *model.NotificationDefinition,
) (*model.NotificationDefinition, error) {
	data, err := encodeNotificationData(definition)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	err = r.withTx(ctx, func(tx base) error {
		found, err := tx.update(ctx, "notification_definitions",
			"UPDATE notification_definitions SET name = ?, description = ?, valid = ?, data = ? WHERE id = ?",
			definition.ID, definition.Name, definition.Description, definition.Valid, data, definition.ID)
		if err != nil {
			return translateNotificationDefinitionErr(err, definition.Name)
		}

		if !found {
			return apperror.NotFound("notification", strconv.FormatUint(uint64(definition.ID), 10))
		}

		return replaceNotificationMessages(ctx, tx, definition.ID, definition.Messages)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, definition.ID)
}

func (r *NotificationDefinitionRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		for _, query := range []string{
			"DELETE FROM notification_messages WHERE definition_id = ?",
			"DELETE FROM notification_opt_outs WHERE definition_id = ?",
		} {
			if _, err := tx.exec(ctx, query, id); err != nil {
				return apperror.Internal(err)
			}
		}

		affected, err := tx.exec(ctx, "DELETE FROM notification_definitions WHERE id = ?", id)
		if err != nil {
			return apperror.Internal(err)
		}

		if affected == 0 {
			return apperror.NotFound("notification", strconv.FormatUint(uint64(id), 10))
		}

		return nil
	})
}

// fill reads what a notification says, in the order the languages are kept.
func (r *NotificationDefinitionRepository) fill(ctx context.Context, definition *model.NotificationDefinition) error {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT language, subject, body FROM notification_messages WHERE definition_id = ? ORDER BY language"),
		definition.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	definition.Messages = make([]model.NotificationMessage, 0)

	for rows.Next() {
		var message model.NotificationMessage
		if err := rows.Scan(&message.Language, &message.Subject, &message.Body); err != nil {
			return apperror.Internal(err)
		}

		definition.Messages = append(definition.Messages, message)
	}

	if err := rows.Err(); err != nil {
		return apperror.Internal(err)
	}

	return nil
}

func replaceNotificationMessages(ctx context.Context, tx base, id uint, messages []model.NotificationMessage) error {
	if _, err := tx.exec(ctx, "DELETE FROM notification_messages WHERE definition_id = ?", id); err != nil {
		return apperror.Internal(err)
	}

	for _, message := range messages {
		if _, err := tx.exec(ctx,
			"INSERT INTO notification_messages (definition_id, language, subject, body) VALUES (?, ?, ?, ?)",
			id, message.Language, message.Subject, message.Body); err != nil {
			return apperror.Internal(err)
		}
	}

	return nil
}

func translateNotificationDefinitionErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a notification named %q already exists", name).
			WithCode("notificationNameTaken", name)
	}

	return apperror.Internal(err)
}
