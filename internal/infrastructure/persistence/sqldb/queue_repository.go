package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// QueueRepository stores the queues and which role reaches each.
type QueueRepository struct {
	base
}

// NewQueueRepository creates a queue repository for the given dialect.
func NewQueueRepository(db DB, dialect string) *QueueRepository {
	return &QueueRepository{base{db: db, dialect: dialect}}
}

var _ repository.QueueRepository = (*QueueRepository)(nil)

const queueColumns = "id, name, parent_id, description, follow_up, unlock_minutes, valid, service_level_id, portal"

func (r *QueueRepository) GetAll(ctx context.Context) ([]*model.Queue, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+queueColumns+" FROM queues ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	queues := make([]*model.Queue, 0)

	for rows.Next() {
		queue, scanErr := scanQueue(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		queues = append(queues, queue)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	// After the rows are closed: a second query open on the same connection while
	// the first is still being read is not portable.
	access, err := r.accessOf(ctx, "SELECT queue_id, role_id, access FROM queue_roles ORDER BY queue_id, role_id")
	if err != nil {
		return nil, err
	}

	for _, queue := range queues {
		queue.Access = access[queue.ID]
	}

	return queues, nil
}

func (r *QueueRepository) GetByID(ctx context.Context, id uint) (*model.Queue, error) {
	queue, err := scanQueue(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+queueColumns+" FROM queues WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("queue", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	access, err := r.accessOf(ctx,
		r.rebind("SELECT queue_id, role_id, access FROM queue_roles WHERE queue_id = ? ORDER BY role_id"), id)
	if err != nil {
		return nil, err
	}

	queue.Access = access[queue.ID]

	return queue, nil
}

// Save stores a new queue and its access in one transaction, because to whoever
// filled in the form they are one thing: a queue committed without the roles that
// were meant to reach it is a queue nobody asked for.
//
// The address, the signature and what a new ticket's sender is told belong to
// the phases that send mail, and are written empty until then; the two texts
// have no default because MySQL allows none on a text column.
func (r *QueueRepository) Save(ctx context.Context, queue *model.Queue) (*model.Queue, error) {
	var id uint

	err := r.withTx(ctx, func(tx base) error {
		newID, err := tx.insert(ctx,
			"INSERT INTO queues (name, parent_id, description, signature, auto_reply, follow_up, unlock_minutes, valid, "+
				"service_level_id, portal) VALUES (?, ?, ?, '', '', ?, ?, ?, ?, ?)",
			queue.Name, queue.ParentID, queue.Description, string(queue.FollowUp), queue.UnlockMinutes, queue.Valid,
			queue.ServiceLevelID, queue.Portal)
		if err != nil {
			return translateQueueErr(err, queue.Name)
		}

		id = newID

		return replaceAccess(ctx, tx, newID, queue.Access)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, id)
}

// Update changes a queue and replaces its access, in one transaction for the
// reason Save gives.
func (r *QueueRepository) Update(ctx context.Context, queue *model.Queue) (*model.Queue, error) {
	err := r.withTx(ctx, func(tx base) error {
		found, err := tx.update(ctx, "queues",
			"UPDATE queues SET name = ?, parent_id = ?, description = ?, follow_up = ?, unlock_minutes = ?, valid = ?, "+
				"service_level_id = ?, portal = ? WHERE id = ?",
			queue.ID,
			queue.Name, queue.ParentID, queue.Description, string(queue.FollowUp), queue.UnlockMinutes, queue.Valid,
			queue.ServiceLevelID, queue.Portal, queue.ID)
		if err != nil {
			return translateQueueErr(err, queue.Name)
		}

		if !found {
			return apperror.NotFound("queue", strconv.FormatUint(uint64(queue.ID), 10))
		}

		return replaceAccess(ctx, tx, queue.ID, queue.Access)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, queue.ID)
}

func (r *QueueRepository) Followers(ctx context.Context, id uint) ([]uint, error) {
	return r.ids(ctx, "SELECT user_id FROM user_queues WHERE queue_id = ? ORDER BY user_id", id)
}

func (r *QueueRepository) FollowedBy(ctx context.Context, userID uint) ([]uint, error) {
	return r.ids(ctx, "SELECT queue_id FROM user_queues WHERE user_id = ? ORDER BY queue_id", userID)
}

func (r *QueueRepository) SetFollowed(ctx context.Context, userID uint, queueIDs []uint) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := tx.exec(ctx, "DELETE FROM user_queues WHERE user_id = ?", userID); err != nil {
			return apperror.Internal(err)
		}

		for _, id := range queueIDs {
			if _, err := tx.exec(ctx, "INSERT INTO user_queues (user_id, queue_id) VALUES (?, ?)", userID, id); err != nil {
				return apperror.Internal(err)
			}
		}

		return nil
	})
}

func (r *QueueRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := tx.exec(ctx, "DELETE FROM queue_roles WHERE queue_id = ?", id); err != nil {
			return apperror.Internal(err)
		}

		if _, err := tx.exec(ctx, "DELETE FROM user_queues WHERE queue_id = ?", id); err != nil {
			return apperror.Internal(err)
		}

		affected, err := tx.exec(ctx, "DELETE FROM queues WHERE id = ?", id)
		if err != nil {
			return apperror.Internal(err)
		}

		if affected == 0 {
			return apperror.NotFound("queue", strconv.FormatUint(uint64(id), 10))
		}

		return nil
	})
}

func (r *QueueRepository) CountTickets(ctx context.Context, id uint) (int, error) {
	return r.count(ctx, "SELECT COUNT(*) FROM tickets WHERE queue_id = ?", id)
}

func (r *QueueRepository) CountChildren(ctx context.Context, id uint) (int, error) {
	return r.count(ctx, "SELECT COUNT(*) FROM queues WHERE parent_id = ?", id)
}

func (r *QueueRepository) count(ctx context.Context, query string, id uint) (int, error) {
	var count int

	if err := r.db.QueryRowContext(ctx, r.rebind(query), id).Scan(&count); err != nil {
		return 0, apperror.Internal(err)
	}

	return count, nil
}

// accessOf reads queue_roles rows into each queue's access, by the queue's id.
func (r *QueueRepository) accessOf(ctx context.Context, query string, args ...any) (map[uint][]model.QueueAccess, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	access := map[uint][]model.QueueAccess{}

	for rows.Next() {
		var (
			queueID uint
			entry   model.QueueAccess
			level   string
		)

		if err := rows.Scan(&queueID, &entry.RoleID, &level); err != nil {
			return nil, apperror.Internal(err)
		}

		entry.Level = model.AccessLevel(level)
		access[queueID] = append(access[queueID], entry)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return access, nil
}

func scanQueue(row scanner) (*model.Queue, error) {
	var (
		queue    model.Queue
		parent   sql.NullInt64
		level    sql.NullInt64
		followUp string
	)

	err := row.Scan(&queue.ID, &queue.Name, &parent, &queue.Description, &followUp, &queue.UnlockMinutes, &queue.Valid,
		&level, &queue.Portal)
	if err != nil {
		return nil, err
	}

	if parent.Valid {
		id := uint(parent.Int64)
		queue.ParentID = &id
	}

	if level.Valid {
		id := uint(level.Int64)
		queue.ServiceLevelID = &id
	}

	queue.FollowUp = model.FollowUp(followUp)

	return &queue, nil
}

func replaceAccess(ctx context.Context, tx base, queueID uint, access []model.QueueAccess) error {
	if _, err := tx.exec(ctx, "DELETE FROM queue_roles WHERE queue_id = ?", queueID); err != nil {
		return apperror.Internal(err)
	}

	for _, entry := range access {
		_, err := tx.exec(ctx, "INSERT INTO queue_roles (queue_id, role_id, access) VALUES (?, ?, ?)",
			queueID, entry.RoleID, string(entry.Level))
		if err != nil {
			return apperror.Internal(err)
		}
	}

	return nil
}

func translateQueueErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a queue named %q already exists", name).WithCode("queueNameTaken", name)
	}

	return apperror.Internal(err)
}
