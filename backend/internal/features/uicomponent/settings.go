package uicomponent

import "context"

// Settings supplies the runtime switch chat.ui_components_enabled. The settings
// area owns the stored value; this area only reads it, once per request, so a
// change applies without a restart.
type Settings interface {
	UIComponentsEnabled(ctx context.Context) bool
}

// SettingsFunc adapts a function to Settings.
type SettingsFunc func(ctx context.Context) bool

// UIComponentsEnabled implements Settings.
func (f SettingsFunc) UIComponentsEnabled(ctx context.Context) bool { return f(ctx) }

// alwaysOn is the default: the documented default of the setting is true.
type alwaysOn struct{}

func (alwaysOn) UIComponentsEnabled(context.Context) bool { return true }

// AlwaysEnabled is the Settings used when the settings area is not wired.
func AlwaysEnabled() Settings { return alwaysOn{} }
