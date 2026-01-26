package user

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// The area depends on three capabilities owned by other areas. They are
// supplied through MountWith; a nil member means "not available" and the
// endpoint or operation that needs it answers as the spec prescribes.

// ErrUploadNotFound is returned by FileReader when the upload does not exist.
var ErrUploadNotFound = errors.New("user: upload not found")

// Upload is an opened stored file.
type Upload struct {
	Body        io.ReadCloser
	ContentType string
	// Size is the length in bytes; zero or negative means unknown.
	Size int64
	// ModTime is the last modification time; the zero value means unknown.
	ModTime  time.Time
	FileName string
}

// FileReader opens the bytes of an upload on behalf of its owner. A missing
// upload yields ErrUploadNotFound.
type FileReader interface {
	OpenUpload(ctx context.Context, ownerUserID uint, uploadID string) (*Upload, error)
}

// ImageChecker decides whether an upload may be bound as an avatar: it must be
// owned by ownerUserID, active, and a valid image. A missing upload is simply
// "not valid". It runs inside the transaction that writes the avatar, and
// implementations should lock the file record through tx so the file cannot
// vanish between the check and the write.
type ImageChecker interface {
	IsActiveImage(ctx context.Context, tx *gorm.DB, ownerUserID uint, uploadID string) (bool, error)
}

// DayActivity is the usage of one calendar day.
type DayActivity struct {
	// Date is "YYYY-MM-DD".
	Date     string
	Requests int64
	Tokens   int64
}

// ActivitySource aggregates a user's usage per day over [from, to), both
// "YYYY-MM-DD". Days without usage may be omitted.
type ActivitySource interface {
	DailyActivity(ctx context.Context, userID uint, from, to string) ([]DayActivity, error)
}

// Billing is the billing area as the administration views and edits see it.
// A nil member makes the area behave as if billing mode were "self" with no
// subscription data.
type Billing interface {
	// Mode returns the global billing mode: "self", "usage" or "period".
	Mode(ctx context.Context) string
	// Subscriptions returns the current subscription of each user. A user
	// without an entry has none.
	Subscriptions(ctx context.Context, userIDs []uint) (map[uint]SubscriptionInfo, error)
	// Accounts returns the billing account of each user; users without an
	// entry have no account.
	Accounts(ctx context.Context, userIDs []uint) (map[uint]BillingAccountInfo, error)
	// AssignPlan replaces the user's subscription with the plan of the given
	// code. Invalid plans or expiries are reported as typed errors.
	AssignPlan(ctx context.Context, userID uint, planCode string, expiresAt *time.Time) error
}

// Auditor writes audit entries for the calling request. The audit area's
// recorder satisfies it.
type Auditor interface {
	RecordRequest(c *gin.Context, action, resource, resourceID string, detail any)
}

// Ports groups the optional collaborators.
type Ports struct {
	// Files serves avatar bytes. Nil makes the avatar endpoint answer 404.
	Files FileReader
	// Images validates "file:" avatars on update. Nil rejects every such update.
	Images ImageChecker
	// Activity feeds the activity endpoint. Nil selects the built-in reader of
	// the billing usage ledger.
	Activity ActivitySource
	// OnSettingsWritten, when set, is called after a settings write was
	// committed and the cache refreshed.
	OnSettingsWritten WriteHook
	// Billing supplies subscription and balance data and plan changes.
	Billing Billing
	// Audit receives the audit entries of the administration routes. The
	// default records into the audit tables.
	Audit Auditor
	// OmitSettingsRoutes leaves GET and PATCH /user/settings unregistered. Use it
	// when another area (the standalone usersettings area) already serves them;
	// two areas must never register the same route.
	OmitSettingsRoutes bool
}
