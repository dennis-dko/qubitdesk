package v1

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/interface/api/v1/rest"
)

// Handlers bundles the handlers a route registration needs.
type Handlers struct {
	Auth     *rest.AuthHandler
	Users    *rest.UserHandler
	Roles    *rest.RoleHandler
	Catalog  *rest.TicketCatalogHandler
	Queues   *rest.QueueHandler
	Tickets  *rest.TicketHandler
	Customer *rest.CustomerHandler
	Me       *rest.MeHandler
	Settings *rest.SettingsHandler
	Tokens   *rest.APITokenHandler
	LDAPSync *rest.LDAPSyncHandler
	Setup    *rest.SetupHandler
	Passkeys *rest.PasskeyHandler
	Logs     *rest.LogHandler
	Restart  *rest.RestartHandler
	Update   *rest.UpdateHandler
	Holidays *rest.HolidayHandler
	OpenID   *rest.OpenIDHandler
	Sheets   *rest.SheetHandler
	Exports  *rest.ExportHandler
	AdminLog *rest.AdminLogHandler
	Push     *rest.PushHandler

	SysConfig *rest.SysConfigHandler
	Calendars *rest.CalendarHandler
}

// RegisterRoutes registers all v1 routes.
//
// The routes are declared explicitly rather than with GoFr's AddRESTHandlers:
// that helper generates CRUD straight against a database table by reflecting
// over a struct, which would bypass the domain and application layers this
// project is built around.
//
// Authorization is likewise not attached here but inside each handler, because
// several rules depend on the resource rather than the route: a ticket is
// reached through the queue it is in, and which queue that is is not known
// until the ticket has been read.
func RegisterRoutes(app *gofr.App, h Handlers) {
	const base = "/api/v1"

	// Sign-in is the one endpoint that must work without a session.
	app.POST(base+"/auth/login", h.Auth.Login)

	// Reachable without a session, because this is how a session begins. The
	// support endpoint tells the interface whether to offer passkeys at all:
	// browsers expose WebAuthn only on HTTPS or localhost.
	app.GET(base+"/auth/passkey", h.Passkeys.Support)
	app.POST(base+"/auth/passkey/login", h.Passkeys.BeginLogin)
	app.PUT(base+"/auth/passkey/login", h.Passkeys.FinishLogin)
	app.POST(base+"/auth/logout", h.Auth.Logout)
	app.POST(base+"/auth/token", h.Auth.TokenSignIn)

	// Signing in through the provider the installation configured: whether it is
	// offered, going there, and finishing with the code it sends the browser back
	// with - all before there is a session, as a passkey sign-in is.
	app.GET(base+"/auth/openid", h.OpenID.Offer)
	app.POST(base+"/auth/openid", h.OpenID.BeginSignIn)
	app.POST(base+"/auth/openid/finish", h.OpenID.Finish)

	// And the caller's own link to their identity there.
	app.GET(base+"/me/openid", h.OpenID.Linked)
	app.POST(base+"/me/openid", h.OpenID.BeginLink)
	app.DELETE(base+"/me/openid", h.OpenID.Unlink)
	app.GET(base+"/auth/kerberos", h.Auth.KerberosSupport)
	app.POST(base+"/auth/kerberos", h.Auth.KerberosLogin)
	app.GET(base+"/languages", h.Auth.Languages)

	// Branding is readable without a session: the sign-in screen shows the
	// instance's own title and logo before anyone has authenticated.
	app.GET(base+"/branding", h.Settings.Branding)

	// Also readable without a session, so the sign-in screen can say why nothing
	// works rather than letting requests fail without explanation.
	app.GET(base+"/maintenance", h.Settings.Maintenance)
	app.PUT(base+"/settings/maintenance", h.Settings.SaveMaintenance)

	app.PUT(base+"/settings/branding", h.Settings.SaveBranding)
	app.GET(base+"/settings/ldap", h.Settings.LDAP)
	app.PUT(base+"/settings/ldap", h.Settings.SaveLDAP)
	app.POST(base+"/settings/ldap/test", h.Settings.TestLDAP)
	app.GET(base+"/settings/openid", h.Settings.OpenID)
	app.PUT(base+"/settings/openid", h.Settings.SaveOpenID)
	app.POST(base+"/settings/openid/test", h.Settings.TestOpenID)
	app.POST(base+"/settings/ldap/sync/preview", h.LDAPSync.Preview)
	app.POST(base+"/settings/ldap/sync", h.LDAPSync.Run)
	app.GET(base+"/settings/ldap/sync/runs", h.LDAPSync.Runs)
	app.GET(base+"/setup", h.Setup.State)
	app.POST(base+"/setup/complete", h.Setup.Complete)
	// Answered before anybody is signed in, which is the one thing that makes
	// it different from every other route here. See SetupHandler.Claim.
	app.POST(base+"/setup/claim", h.Setup.Claim)
	app.GET(base+"/settings/operational", h.Settings.Operational)
	app.PUT(base+"/settings/operational", h.Settings.SaveOperational)
	app.GET(base+"/settings/timezone", h.Settings.Timezone)
	app.PUT(base+"/settings/timezone", h.Settings.SaveTimezone)
	app.GET(base+"/settings/holidays", h.Holidays.Installation)
	app.PUT(base+"/settings/holidays", h.Holidays.SetInstallation)
	app.GET(base+"/settings/datasource", h.Settings.Datasource)
	app.PUT(base+"/settings/datasource", h.Settings.SaveDatasource)
	app.POST(base+"/settings/datasource/test", h.Settings.TestDatasource)
	app.GET(base+"/settings/telemetry", h.Settings.Telemetry)
	app.PUT(base+"/settings/telemetry", h.Settings.SaveTelemetry)

	// Whether notifications are pushed to browsers at all - shipped off, because
	// every push leaves through the push service of the browser's maker.
	app.GET(base+"/settings/push", h.Push.Switch)
	app.PUT(base+"/settings/push", h.Push.SaveSwitch)

	// The system configuration: options with defaults, an installation storing
	// only what it changed. Under /settings/ because its card is on that screen,
	// which maintenance mode leaves reachable; the list is read by every account
	// signed in, which is handed the external options alone.
	app.GET(base+"/settings/sysconfig", h.SysConfig.List)
	app.PUT(base+"/settings/sysconfig/{name}", h.SysConfig.Set)
	app.DELETE(base+"/settings/sysconfig/{name}", h.SysConfig.Reset)

	// What is waiting for a restart, and the restart itself. Under /settings/
	// like the rest, which also keeps it reachable during maintenance mode - the
	// settings that need a restart are exactly the ones somebody is likely to be
	// changing while the installation is out of service.
	app.GET(base+"/settings/restart", h.Restart.State)
	app.POST(base+"/settings/restart", h.Restart.Restart)

	// Guarded harder than the rest of Settings: this one replaces the bytes that
	// will be executed after the next start. See UpdateHandler.
	app.GET(base+"/settings/update", h.Update.State)
	app.POST(base+"/settings/update", h.Update.Apply)
	app.POST(base+"/settings/update/check", h.Update.Check)

	// The process log. Under /admin rather than /settings because it changes
	// nothing - and behind whoever administers the installation, because it
	// carries everything the process has written.
	app.GET(base+"/admin/logs", h.Logs.Logs)

	// Who changed how the installation is run. Under /admin for the same reason,
	// and for whoever administers it: a record of administration is theirs to read.
	app.GET(base+"/admin/changes", h.AdminLog.Changes)

	app.GET(base+"/me", h.Me.Me)
	app.PUT(base+"/me/password", h.Me.ChangePassword)
	app.PUT(base+"/me/language", h.Auth.SetLanguage)
	app.PUT(base+"/me/theme", h.Auth.SetTheme)
	app.PUT(base+"/me/timezone", h.Auth.SetTimezone)
	app.PUT(base+"/me/tour", h.Auth.SetTourSeen)

	// Everything held about one's own account, for handing over.
	app.GET(base+"/me/export", h.Exports.Account)

	app.GET(base+"/me/push", h.Push.State)
	app.POST(base+"/me/push", h.Push.Subscribe)
	app.DELETE(base+"/me/push", h.Push.Unsubscribe)

	app.GET(base+"/me/address", h.Auth.Address)

	// Where the Android app of this installation's release is downloaded, and the
	// same as a code for a phone's camera.
	app.GET(base+"/me/android", h.Update.Android)

	app.GET(base+"/me/passkeys", h.Passkeys.List)
	app.POST(base+"/me/passkeys/register", h.Passkeys.BeginRegistration)
	app.PUT(base+"/me/passkeys/register", h.Passkeys.FinishRegistration)
	app.DELETE(base+"/me/passkeys/{id}", h.Passkeys.Delete)
	app.POST(base+"/me/totp", h.Auth.BeginTOTP)
	app.PUT(base+"/me/totp", h.Auth.ConfirmTOTP)
	app.DELETE(base+"/me/totp", h.Auth.DisableTOTP)
	app.GET(base+"/me/tokens", h.Tokens.List)
	app.POST(base+"/me/tokens", h.Tokens.Create)
	app.DELETE(base+"/me/tokens/{id}", h.Tokens.Revoke)

	// Ahead of the {id} routes: "export" is not an id, and this router matches
	// the first pattern that fits.
	app.GET(base+"/users/export", h.Sheets.ExportUsers)
	app.POST(base+"/users/import", h.Sheets.ImportUsers)

	app.GET(base+"/users", h.Users.List)
	app.GET(base+"/users/{id}", h.Users.Get)
	app.POST(base+"/users", h.Users.Create)
	app.PUT(base+"/users/{id}", h.Users.Update)
	app.DELETE(base+"/users/{id}", h.Users.Delete)
	app.PUT(base+"/users/{id}/role", h.Users.AssignRole)
	app.PUT(base+"/users/{id}/password", h.Users.SetPassword)

	// Ahead of the {id} routes, for the same reason the others are: "export" is
	// not an id.
	app.GET(base+"/roles/export", h.Sheets.ExportRoles)
	app.POST(base+"/roles/import", h.Sheets.ImportRoles)

	app.GET(base+"/roles", h.Roles.List)
	app.GET(base+"/roles/{id}", h.Roles.Get)
	app.POST(base+"/roles", h.Roles.Create)
	app.PUT(base+"/roles/{id}", h.Roles.Update)
	app.DELETE(base+"/roles/{id}", h.Roles.Delete)
	app.GET(base+"/permissions", h.Roles.Permissions)

	// The tickets, reached through their queues. assignees is read before
	// {id}, as the export routes are: the router matches the first pattern that
	// fits, and "assignees" read as an id is the failure to avoid.
	app.GET(base+"/tickets", h.Tickets.List)
	app.POST(base+"/tickets/{id}/articles/{article}/attachments", h.Tickets.UploadAttachment)
	app.GET(base+"/tickets/{id}/attachments/{attachment}", h.Tickets.DownloadAttachment)
	app.GET(base+"/tickets/assignees", h.Tickets.Assignees)
	app.POST(base+"/tickets", h.Tickets.Create)
	app.GET(base+"/tickets/{id}", h.Tickets.Get)
	app.PUT(base+"/tickets/{id}", h.Tickets.Update)
	app.DELETE(base+"/tickets/{id}", h.Tickets.Delete)
	app.GET(base+"/tickets/{id}/articles", h.Tickets.Articles)
	app.POST(base+"/tickets/{id}/articles", h.Tickets.AddArticle)
	app.GET(base+"/tickets/{id}/history", h.Tickets.History)
	app.PUT(base+"/tickets/{id}/watch", h.Tickets.Watch)
	app.DELETE(base+"/tickets/{id}/watch", h.Tickets.Unwatch)

	// Who tickets come from.
	app.GET(base+"/organisations", h.Customer.ListOrganisations)
	app.GET(base+"/organisations/{id}", h.Customer.GetOrganisation)
	app.POST(base+"/organisations", h.Customer.CreateOrganisation)
	app.PUT(base+"/organisations/{id}", h.Customer.UpdateOrganisation)
	app.DELETE(base+"/organisations/{id}", h.Customer.DeleteOrganisation)
	app.GET(base+"/contacts", h.Customer.ListContacts)
	app.GET(base+"/contacts/{id}", h.Customer.GetContact)
	app.POST(base+"/contacts", h.Customer.CreateContact)
	app.PUT(base+"/contacts/{id}", h.Customer.UpdateContact)
	app.DELETE(base+"/contacts/{id}", h.Customer.DeleteContact)

	// Where tickets are sorted to, and which role reaches each queue.
	app.GET(base+"/queues", h.Queues.List)
	app.GET(base+"/queues/{id}", h.Queues.Get)
	app.POST(base+"/queues", h.Queues.Create)
	app.PUT(base+"/queues/{id}", h.Queues.Update)
	app.DELETE(base+"/queues/{id}", h.Queues.Delete)

	// What tickets are sorted by: the states, the priorities and the types, each
	// shipped with values a service desk starts from.
	app.GET(base+"/ticket-states", h.Catalog.ListStates)
	app.GET(base+"/ticket-states/{id}", h.Catalog.GetState)
	app.POST(base+"/ticket-states", h.Catalog.CreateState)
	app.PUT(base+"/ticket-states/{id}", h.Catalog.UpdateState)
	app.DELETE(base+"/ticket-states/{id}", h.Catalog.DeleteState)
	app.GET(base+"/ticket-priorities", h.Catalog.ListPriorities)
	app.GET(base+"/ticket-priorities/{id}", h.Catalog.GetPriority)
	app.POST(base+"/ticket-priorities", h.Catalog.CreatePriority)
	app.PUT(base+"/ticket-priorities/{id}", h.Catalog.UpdatePriority)
	app.DELETE(base+"/ticket-priorities/{id}", h.Catalog.DeletePriority)
	app.GET(base+"/calendars", h.Calendars.List)
	app.GET(base+"/calendars/{id}", h.Calendars.Get)
	app.POST(base+"/calendars", h.Calendars.Create)
	app.PUT(base+"/calendars/{id}", h.Calendars.Update)
	app.DELETE(base+"/calendars/{id}", h.Calendars.Delete)

	app.GET(base+"/ticket-types", h.Catalog.ListTypes)
	app.GET(base+"/ticket-types/{id}", h.Catalog.GetType)
	app.POST(base+"/ticket-types", h.Catalog.CreateType)
	app.PUT(base+"/ticket-types/{id}", h.Catalog.UpdateType)
	app.DELETE(base+"/ticket-types/{id}", h.Catalog.DeleteType)
}
