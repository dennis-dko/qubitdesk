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

// catalog is what reading and writing one of the tables tickets are sorted by -
// the states, the priorities, the types - has in common. Each table hands it its
// own statements written out whole, so nothing is put together from a table's
// name, together with the few functions that know its columns.
//
// is_system is written by the seeding migrations and by nothing here: a value
// made through the application is the installation's own, and a shipped one
// stays shipped.
type catalog[T any] struct {
	base

	table string // for the existence check behind an update that changed nothing
	stmts catalogStatements

	// missing is the refusal for an id no row has, naming the kind of record
	// where the interface's words for it are looked up.
	missing func(id string) error

	// scan reads a row in the order the statements' SELECT names the columns.
	scan func(row scanner) (*T, error)

	// written is what INSERT and UPDATE write, in the order both name them; the
	// UPDATE takes the id after them.
	written func(item *T) []any

	id   func(item *T) uint
	name func(item *T) string

	// taken is the refusal for a name another row already has.
	taken func(name string) error
}

// catalogStatements are one table's statements.
type catalogStatements struct {
	all, byID, insert, update, delete, countTickets string
}

func (c *catalog[T]) GetAll(ctx context.Context) ([]*T, error) {
	rows, err := c.db.QueryContext(ctx, c.stmts.all)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]*T, 0)

	for rows.Next() {
		item, scanErr := c.scan(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return items, nil
}

func (c *catalog[T]) GetByID(ctx context.Context, id uint) (*T, error) {
	item, err := c.scan(c.db.QueryRowContext(ctx, c.rebind(c.stmts.byID), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, c.missing(strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return item, nil
}

func (c *catalog[T]) Save(ctx context.Context, item *T) (*T, error) {
	id, err := c.insert(ctx, c.stmts.insert, c.written(item)...)
	if err != nil {
		return nil, c.writeProblem(err, item)
	}

	return c.GetByID(ctx, id)
}

func (c *catalog[T]) Update(ctx context.Context, item *T) (*T, error) {
	id := c.id(item)

	found, err := c.update(ctx, c.table, c.stmts.update, id, append(c.written(item), id)...)
	if err != nil {
		return nil, c.writeProblem(err, item)
	}

	if !found {
		return nil, c.missing(strconv.FormatUint(uint64(id), 10))
	}

	return c.GetByID(ctx, id)
}

func (c *catalog[T]) Delete(ctx context.Context, id uint) error {
	affected, err := c.exec(ctx, c.stmts.delete, id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return c.missing(strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

func (c *catalog[T]) CountTickets(ctx context.Context, id uint) (int, error) {
	var count int

	if err := c.db.QueryRowContext(ctx, c.rebind(c.stmts.countTickets), id).Scan(&count); err != nil {
		return 0, apperror.Internal(err)
	}

	return count, nil
}

func (c *catalog[T]) writeProblem(err error, item *T) error {
	if isUniqueViolation(err) {
		return c.taken(c.name(item))
	}

	return apperror.Internal(err)
}

// TicketStateRepository stores the states a ticket can be in, in the order they
// were made - which, for the shipped ones, is the order a ticket passes through
// them.
type TicketStateRepository struct {
	catalog[model.TicketState]
}

// NewTicketStateRepository creates a state repository for the given dialect.
func NewTicketStateRepository(db DB, dialect string) *TicketStateRepository {
	return &TicketStateRepository{catalog[model.TicketState]{
		base:    base{db: db, dialect: dialect},
		table:   "ticket_states",
		missing: func(id string) error { return apperror.NotFound("state", id) },
		stmts: catalogStatements{
			all:          "SELECT id, name, state_type, description, valid, is_system FROM ticket_states ORDER BY id",
			byID:         "SELECT id, name, state_type, description, valid, is_system FROM ticket_states WHERE id = ?",
			insert:       "INSERT INTO ticket_states (name, state_type, description, valid) VALUES (?, ?, ?, ?)",
			update:       "UPDATE ticket_states SET name = ?, state_type = ?, description = ?, valid = ? WHERE id = ?",
			delete:       "DELETE FROM ticket_states WHERE id = ?",
			countTickets: "SELECT COUNT(*) FROM tickets WHERE state_id = ?",
		},
		scan: func(row scanner) (*model.TicketState, error) {
			var (
				state model.TicketState
				kind  string
			)

			if err := row.Scan(&state.ID, &state.Name, &kind, &state.Description, &state.Valid, &state.IsSystem); err != nil {
				return nil, err
			}

			state.Type = model.StateType(kind)

			return &state, nil
		},
		written: func(state *model.TicketState) []any {
			return []any{state.Name, string(state.Type), state.Description, state.Valid}
		},
		id:   func(state *model.TicketState) uint { return state.ID },
		name: func(state *model.TicketState) string { return state.Name },
		taken: func(name string) error {
			return apperror.Conflictf("a state named %q already exists", name).WithCode("stateNameTaken", name)
		},
	}}
}

var _ repository.TicketStateRepository = (*TicketStateRepository)(nil)

// TicketPriorityRepository stores how urgent a ticket can be, the most urgent
// first.
type TicketPriorityRepository struct {
	catalog[model.TicketPriority]
}

// NewTicketPriorityRepository creates a priority repository for the given
// dialect.
func NewTicketPriorityRepository(db DB, dialect string) *TicketPriorityRepository {
	return &TicketPriorityRepository{catalog[model.TicketPriority]{
		base:    base{db: db, dialect: dialect},
		table:   "ticket_priorities",
		missing: func(id string) error { return apperror.NotFound("priority", id) },
		stmts: catalogStatements{
			all: "SELECT id, name, level, description, valid, is_system FROM ticket_priorities " +
				"ORDER BY level, id",
			byID:         "SELECT id, name, level, description, valid, is_system FROM ticket_priorities WHERE id = ?",
			insert:       "INSERT INTO ticket_priorities (name, level, description, valid) VALUES (?, ?, ?, ?)",
			update:       "UPDATE ticket_priorities SET name = ?, level = ?, description = ?, valid = ? WHERE id = ?",
			delete:       "DELETE FROM ticket_priorities WHERE id = ?",
			countTickets: "SELECT COUNT(*) FROM tickets WHERE priority_id = ?",
		},
		scan: func(row scanner) (*model.TicketPriority, error) {
			var priority model.TicketPriority

			err := row.Scan(&priority.ID, &priority.Name, &priority.Level, &priority.Description,
				&priority.Valid, &priority.IsSystem)
			if err != nil {
				return nil, err
			}

			return &priority, nil
		},
		written: func(priority *model.TicketPriority) []any {
			return []any{priority.Name, priority.Level, priority.Description, priority.Valid}
		},
		id:   func(priority *model.TicketPriority) uint { return priority.ID },
		name: func(priority *model.TicketPriority) string { return priority.Name },
		taken: func(name string) error {
			return apperror.Conflictf("a priority named %q already exists", name).WithCode("priorityNameTaken", name)
		},
	}}
}

var _ repository.TicketPriorityRepository = (*TicketPriorityRepository)(nil)

// TicketTypeRepository stores the kinds of ticket an installation tells apart.
type TicketTypeRepository struct {
	catalog[model.TicketType]
}

// NewTicketTypeRepository creates a type repository for the given dialect.
func NewTicketTypeRepository(db DB, dialect string) *TicketTypeRepository {
	return &TicketTypeRepository{catalog[model.TicketType]{
		base:    base{db: db, dialect: dialect},
		table:   "ticket_types",
		missing: func(id string) error { return apperror.NotFound("type", id) },
		stmts: catalogStatements{
			all:          "SELECT id, name, description, valid, is_system FROM ticket_types ORDER BY id",
			byID:         "SELECT id, name, description, valid, is_system FROM ticket_types WHERE id = ?",
			insert:       "INSERT INTO ticket_types (name, description, valid) VALUES (?, ?, ?)",
			update:       "UPDATE ticket_types SET name = ?, description = ?, valid = ? WHERE id = ?",
			delete:       "DELETE FROM ticket_types WHERE id = ?",
			countTickets: "SELECT COUNT(*) FROM tickets WHERE type_id = ?",
		},
		scan: func(row scanner) (*model.TicketType, error) {
			var kind model.TicketType

			if err := row.Scan(&kind.ID, &kind.Name, &kind.Description, &kind.Valid, &kind.IsSystem); err != nil {
				return nil, err
			}

			return &kind, nil
		},
		written: func(kind *model.TicketType) []any {
			return []any{kind.Name, kind.Description, kind.Valid}
		},
		id:   func(kind *model.TicketType) uint { return kind.ID },
		name: func(kind *model.TicketType) string { return kind.Name },
		taken: func(name string) error {
			return apperror.Conflictf("a type named %q already exists", name).WithCode("typeNameTaken", name)
		},
	}}
}

var _ repository.TicketTypeRepository = (*TicketTypeRepository)(nil)
