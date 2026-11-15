package repository

import "context"

// SettingsRepository stores the instance settings as key/value pairs.
type SettingsRepository interface {
	// Get returns the value, or an empty string when the key is unset. A
	// missing setting is a normal state and not reported as an error.
	Get(ctx context.Context, key string) (string, error)

	// Lookup is Get for a caller that has to tell a value stored as nothing
	// from no value at all.
	Lookup(ctx context.Context, key string) (value string, stored bool, err error)

	Set(ctx context.Context, key, value string) error

	// Delete removes the key, so that whatever reads it falls back to its
	// default; a key that is not there is not an error.
	Delete(ctx context.Context, key string) error

	GetAll(ctx context.Context) (map[string]string, error)
}
