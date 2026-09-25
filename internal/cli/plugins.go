package cli

import (
	"context"
	"log/slog"

	"github.com/ikigai-db/ikigai-db/internal/plugin"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// The plugins a pipeline can use (FR-16.2, FR-16.1).
//
// A source somebody added as a plugin is a source, so a build that browses one
// should be able to export from it. The same switch governs both this and the
// window — the settings file's — because a plugin turned on in one and not the
// other would be a difference nobody could account for.
//
// Nothing is said when there are none, which is the ordinary case: a command
// line that announced "no plugins" on every run would be a command line nobody
// could read the output of.

// loadPlugins starts what the settings say to start. It never fails a command:
// a plugin that will not load is reported, and the verbs that do not need it go
// on working.
func loadPlugins(ctx context.Context, e *env) *plugin.Set {
	paths, err := store.Resolve()
	if err != nil {
		return nil
	}
	settings, _, err := store.OpenSettings(paths.SettingsFile())
	if err != nil {
		return nil
	}
	set, err := plugin.FromSettings(ctx, settings.Get().Plugins, paths, slog.New(slog.DiscardHandler))
	if err != nil {
		e.sayf("the plugins directory could not be read: %v", err)
		return set
	}
	for name, why := range set.Failed {
		e.sayf("the %s plugin did not load: %v", name, why)
	}
	return set
}
