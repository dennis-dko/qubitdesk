package service

import (
	"context"
	"fmt"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// SysConfigService reads and changes the system configuration: the options the
// model defines with their defaults, and the values an installation chose,
// stored in the settings table under model.SysConfigPrefix.
type SysConfigService struct {
	settings repository.SettingsRepository
	checks   map[string]func(context.Context, string) error
}

// NewSysConfigService keeps its values beside the other settings.
func NewSysConfigService(settings repository.SettingsRepository) *SysConfigService {
	return &SysConfigService{settings: settings}
}

// WithCheck has a value of an option asked of the code that obeys it before it
// is stored, for a value of the right type that code could still not use - a
// state that is not there - and refused as the check refuses it. The default
// is never asked: it is what the code was written against.
func (s *SysConfigService) WithCheck(name string, check func(ctx context.Context, value string) error) *SysConfigService {
	if s.checks == nil {
		s.checks = map[string]func(context.Context, string) error{}
	}

	s.checks[name] = check

	return s
}

// ConfigValue is an option with the value it has here, and whether that value
// is one the installation chose rather than the option's default.
type ConfigValue struct {
	Option   model.ConfigOption
	Value    string
	Modified bool
}

// List is every option the reader may read: all of them for whoever may change
// the installation's settings, the external ones for everybody else.
//
// Read one key at a time rather than the whole settings table, which carries
// the logos as data and is read for every screen that is drawn.
func (s *SysConfigService) List(ctx context.Context, everything bool) ([]ConfigValue, error) {
	values := make([]ConfigValue, 0)

	for _, option := range model.ConfigOptions() {
		if !everything && option.Access != model.ConfigExternal {
			continue
		}

		value, err := s.valueOf(ctx, option)
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

// Set changes an option. A value equal to the default makes it the default
// again rather than a choice that happens to agree with it, so the option goes
// on following the default should a later version change that.
func (s *SysConfigService) Set(ctx context.Context, name string, value any) (ConfigValue, error) {
	option, ok := model.ConfigOptionNamed(name)
	if !ok {
		return ConfigValue{}, apperror.NotFound("option", name)
	}

	canonical, ok := option.Canonical(value)
	if !ok {
		return ConfigValue{}, apperror.InvalidFields("value")
	}

	if canonical == option.Default {
		return s.Reset(ctx, name)
	}

	if check := s.checks[option.Name]; check != nil {
		if err := check(ctx, canonical); err != nil {
			return ConfigValue{}, err
		}
	}

	if err := s.settings.Set(ctx, model.SysConfigPrefix+option.Name, canonical); err != nil {
		return ConfigValue{}, err
	}

	return ConfigValue{Option: option, Value: canonical, Modified: true}, nil
}

// Reset puts an option back to its default.
func (s *SysConfigService) Reset(ctx context.Context, name string) (ConfigValue, error) {
	option, ok := model.ConfigOptionNamed(name)
	if !ok {
		return ConfigValue{}, apperror.NotFound("option", name)
	}

	if err := s.settings.Delete(ctx, model.SysConfigPrefix+option.Name); err != nil {
		return ConfigValue{}, err
	}

	return ConfigValue{Option: option, Value: option.Default}, nil
}

// Bool reads a switch, as the code that obeys it does.
func (s *SysConfigService) Bool(ctx context.Context, name string) (bool, error) {
	option, ok := model.ConfigOptionNamed(name)
	if !ok || option.Type != model.ConfigBool {
		return false, fmt.Errorf("no switch of the system configuration is called %q", name)
	}

	value, err := s.valueOf(ctx, option)
	if err != nil {
		return option.Default == "true", err
	}

	return value.Value == "true", nil
}

// String reads an option of text, as the code that obeys it does.
func (s *SysConfigService) String(ctx context.Context, name string) (string, error) {
	option, ok := model.ConfigOptionNamed(name)
	if !ok || option.Type != model.ConfigString {
		return "", fmt.Errorf("no option of text in the system configuration is called %q", name)
	}

	value, err := s.valueOf(ctx, option)
	if err != nil {
		return option.Default, err
	}

	return value.Value, nil
}

// valueOf is the value an option has here: the one stored, or its default.
func (s *SysConfigService) valueOf(ctx context.Context, option model.ConfigOption) (ConfigValue, error) {
	value, stored, err := s.settings.Lookup(ctx, model.SysConfigPrefix+option.Name)
	if err != nil {
		return ConfigValue{}, err
	}

	if !stored {
		return ConfigValue{Option: option, Value: option.Default}, nil
	}

	return ConfigValue{Option: option, Value: value, Modified: true}, nil
}
