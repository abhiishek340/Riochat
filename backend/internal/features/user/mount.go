// Package user owns per-user preferences (the user_settings table and its
// shared cache), the public avatar endpoint, the daily activity endpoint, the
// validation rules for account fields, and the account-administration
// capabilities that the admin and auth areas call (see Accounts).
package user

import (
	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/platform/capability"
)

// Mount registers the area's routes without optional collaborators: the avatar
// endpoint answers 404 until a FileReader is supplied through MountWith, and
// the activity endpoint reads the billing usage ledger directly.
func Mount(d deps.Deps) error { return MountWith(d, Ports{}) }

// MountWith registers the routes with the given collaborators.
func MountWith(d deps.Deps, ports Ports) error {
	accounts := NewAccounts(d.DB, d.Clock, ports.Images)
	cache := NewSettingsCache(d.DB, d.Cache, d.Logger)
	store := NewSettingsStore(d.DB, d.Clock, cache, ports.OnSettingsWritten, d.Logger)

	var auditor Auditor = audit.NewRecorder(d.DB, d.Logger, d.Clock)
	if ports.Audit != nil {
		auditor = ports.Audit
	}
	mountAdmin(adminDeps{
		routes: d.Gated(capability.MultiUser), accounts: accounts, billing: ports.Billing, audit: auditor,
		clock: d.Clock, cfg: d.Config,
	})

	activity := ports.Activity
	if activity == nil {
		activity = ledgerActivity{db: d.DB}
	}

	d.Routes.Public.GET("/users/:public_id/avatar", avatarHandler(accounts, ports.Files))

	d.Routes.Auth.GET("/user/stats/activity", activityHandler(activity, d.Clock))
	if !ports.OmitSettingsRoutes {
		d.Routes.Auth.GET("/user/settings", getSettingsHandler(store))
		d.Routes.Auth.PATCH("/user/settings", patchSettingsHandler(store))
	}
	return nil
}
