package sqldb

import (
	"context"
	"fmt"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// accountExport is what an account's export reads: a statement for every table
// PurgeUser erases from or takes the account's name off, and for what the
// account wrote - test/export_test.go holds it to the erasure's list.
//
// Every column is named rather than taken with *, because what is left out is the
// point as much as what is put in: a password hash, a second factor's seed, the
// hashes of tokens and sessions, and a passkey's key material are the account's,
// and a file of them lying in somebody's downloads folder is a way in. What is
// handed over about each of those is that it exists, when it was made and when
// it was last used.
//
// What the account wrote in tickets is handed over as where and when, not as the
// text: an answer quotes the customer it answers, and the export of one person
// is not the place for another's words.
var accountExport = []struct {
	file  string
	query string
}{
	{"account", "SELECT u.id, u.name, u.email, COALESCE(r.name, '') AS role, u.language, u.theme, " +
		"u.timezone, u.is_external, u.external_id, u.totp_enabled, u.must_change_password, u.tour_seen " +
		"FROM users u LEFT JOIN roles r ON r.id = u.role_id WHERE u.id = ?"},
	{"owned-tickets", "SELECT t.id, t.number, t.title, s.name AS state, t.created_at " +
		"FROM tickets t LEFT JOIN ticket_states s ON s.id = t.state_id WHERE t.owner_id = ? ORDER BY t.id"},
	{"tickets-responsible-for", "SELECT t.id, t.number, t.title, s.name AS state, t.created_at " +
		"FROM tickets t LEFT JOIN ticket_states s ON s.id = t.state_id WHERE t.responsible_id = ? ORDER BY t.id"},
	{"contact", "SELECT id, organisation_id, name, email, phone, created_at FROM contacts WHERE user_id = ?"},
	{"watched-tickets", "SELECT ticket_id FROM ticket_watchers WHERE user_id = ? ORDER BY ticket_id"},
	{"notifications", "SELECT ticket_id, definition_id, created_at, read_at " +
		"FROM notifications WHERE user_id = ? ORDER BY id"},
	{"queues-told-about", "SELECT queue_id FROM user_queues WHERE user_id = ? ORDER BY queue_id"},
	{"notifications-switched-off", "SELECT definition_id FROM notification_opt_outs WHERE user_id = ? " +
		"ORDER BY definition_id"},
	{"faq-articles-written", "SELECT id, title, updated_at FROM faq_articles WHERE updated_by = ? ORDER BY id"},
	{"api-tokens", "SELECT id, name, prefix, created_at, expires_at, last_used_at " +
		"FROM api_tokens WHERE user_id = ? ORDER BY id"},
	{"passkeys", "SELECT id, name, attestation_type, transports, sign_count, backup_eligible, backed_up, " +
		"created_at, last_used_at FROM passkeys WHERE user_id = ? ORDER BY id"},
	{"sessions", "SELECT created_at, expires_at, last_seen_at, api_token_id " +
		"FROM sessions WHERE user_id = ? ORDER BY created_at"},
	{"push-subscriptions", "SELECT origin, created_at FROM push_subscriptions WHERE user_id = ? ORDER BY id"},
	{"openid-links", "SELECT issuer, subject, created_at FROM openid_identities WHERE user_id = ? ORDER BY id"},
	{"written-articles", "SELECT id, ticket_id, channel, subject, created_at " +
		"FROM articles WHERE sender = 'agent' AND author_id = ? ORDER BY id"},
	{"accounted-time", "SELECT ticket_id, article_id, minutes, created_at " +
		"FROM time_accounting WHERE created_by = ? ORDER BY id"},
	{"config-item-versions-written", "SELECT id, item_id, name, created_at " +
		"FROM config_item_versions WHERE created_by = ? ORDER BY id"},
}

// ExportUser reads everything held about an account, a part per table, each row
// as its columns.
//
// Columns as they are stored rather than as a model would put them: an export is
// an answer to "what do you keep about me", and the stored column is the honest
// answer to it. Only the shapes a driver hands back are evened out - bytes become
// text, a moment becomes RFC 3339 - so the same installation answers the same way
// on all three databases.
func (r *UserRepository) ExportUser(ctx context.Context, userID uint) ([]repository.ExportedPart, error) {
	parts := make([]repository.ExportedPart, 0, len(accountExport))

	for _, part := range accountExport {
		rows, err := r.exportRows(ctx, part.query, userID)
		if err != nil {
			return nil, apperror.Internal(fmt.Errorf("exporting the %s of user %d: %w", part.file, userID, err))
		}

		parts = append(parts, repository.ExportedPart{Name: part.file, Rows: rows})
	}

	return parts, nil
}

// exportRows runs one export statement and reads every row as column and value.
func (r *UserRepository) exportRows(ctx context.Context, query string, userID uint) ([]map[string]any, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(query), userID)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	read := []map[string]any{}

	for rows.Next() {
		values := make([]any, len(columns))
		into := make([]any, len(columns))

		for i := range values {
			into[i] = &values[i]
		}

		if err := rows.Scan(into...); err != nil {
			return nil, err
		}

		row := make(map[string]any, len(columns))

		for i, column := range columns {
			switch value := values[i].(type) {
			case []byte:
				row[column] = string(value)
			case time.Time:
				row[column] = value.UTC().Format(time.RFC3339)
			default:
				row[column] = value
			}
		}

		read = append(read, row)
	}

	return read, rows.Err()
}
