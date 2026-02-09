package user

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

// patchBody is the partial update of an account. A key that is absent leaves
// the field alone; a key holding an empty string sets it to empty or default.
type patchBody struct {
	AvatarURL             *string    `json:"avatarURL" binding:"omitempty,max=2048"`
	DisplayName           *string    `json:"displayName" binding:"omitempty,min=3,max=16"`
	Email                 *string    `json:"email" binding:"omitempty,max=128"`
	Phone                 *string    `json:"phone" binding:"omitempty,max=32"`
	Role                  *string    `json:"role" binding:"omitempty,max=32"`
	Status                *string    `json:"status" binding:"omitempty,max=32"`
	Timezone              *string    `json:"timezone" binding:"omitempty,max=64"`
	Locale                *string    `json:"locale" binding:"omitempty,max=16"`
	ProfilePreferences    *string    `json:"profilePreferences" binding:"omitempty,max=1024"`
	SubscriptionTier      *string    `json:"subscriptionTier" binding:"omitempty,max=32"`
	SubscriptionExpiresAt *time.Time `json:"subscriptionExpiresAt"`
	Reason                string     `json:"reason" binding:"max=255"`
}

// change is the from/to pair an audit entry records for one field.
type change struct {
	From any `json:"from"`
	To   any `json:"to"`
}

func (a *adminAPI) patch(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	var req patchBody
	if !httpx.BindJSONPlain(c, &req) {
		return
	}
	acting, target, ok := a.load(c, id)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	detail := gin.H{}
	var fields UpdateFields
	profileChanged, otherChanged := false, false
	emailReset, phoneReset, rolled := false, false, false
	newRole := ""

	reject := func(status int, err error) bool {
		httpx.Fail(c, status, err)
		return false
	}

	// 1. avatar
	if req.AvatarURL != nil {
		if v := strings.TrimSpace(*req.AvatarURL); v != target.AvatarURL {
			fields.AvatarURL = Some(v)
			detail["avatar_url"] = change{target.AvatarURL, v}
			profileChanged = true
		}
	}
	// 2. display name
	if req.DisplayName != nil {
		v, err := NormalizeDisplayName(*req.DisplayName)
		if err != nil {
			reject(http.StatusBadRequest, err)
			return
		}
		if v != target.DisplayName {
			fields.DisplayName = Some(v)
			detail["display_name"] = change{target.DisplayName, v}
			profileChanged = true
		}
	}
	// 3. email
	if req.Email != nil {
		v, err := NormalizeEmail(*req.Email)
		if err != nil {
			reject(http.StatusBadRequest, err)
			return
		}
		if v != target.Email {
			fields.Email = Some(v)
			fields.EmailVerifiedAt = Some[*time.Time](nil)
			fields.EmailSource = Some(EmailSourceAdminSet)
			detail["email"] = change{target.Email, v}
			emailReset, profileChanged = true, true
		}
	}
	// 4. phone
	if req.Phone != nil {
		v, err := NormalizePhone(*req.Phone)
		if err != nil {
			reject(http.StatusBadRequest, err)
			return
		}
		if v != target.Phone {
			fields.Phone = Some(v)
			fields.PhoneVerifiedAt = Some[*time.Time](nil)
			detail["phone"] = change{target.Phone, v}
			phoneReset, profileChanged = true, true
		}
	}
	// 5. role
	if req.Role != nil {
		role := strings.TrimSpace(*req.Role)
		if role != RoleUser && role != RoleAdmin && role != RoleSuperAdmin {
			reject(http.StatusBadRequest, ErrInvalidRole)
			return
		}
		if role != target.Role {
			if acting.ID == target.ID {
				reject(http.StatusConflict, ErrSelfRoleChange)
				return
			}
			if role == RoleSuperAdmin && acting.Role != RoleSuperAdmin {
				reject(http.StatusForbidden, ErrSuperadminManagement)
				return
			}
			if target.Role == RoleSuperAdmin {
				n, err := a.accounts.CountSuperadmins(ctx)
				if err != nil {
					fail(c, err)
					return
				}
				if n <= 1 {
					reject(http.StatusConflict, ErrLastSuperadminRole)
					return
				}
			}
			fields.Role = Some(role)
			detail["role"] = change{target.Role, role}
			newRole, rolled, profileChanged = role, true, true
		}
	}
	// 6. time zone
	if req.Timezone != nil {
		v, err := NormalizeTimezone(*req.Timezone)
		if err != nil {
			reject(http.StatusBadRequest, err)
			return
		}
		if v != target.Timezone {
			fields.Timezone = Some(v)
			detail["timezone"] = change{target.Timezone, v}
			profileChanged = true
		}
	}
	// 7. locale
	if req.Locale != nil {
		v, err := NormalizeLocale(*req.Locale)
		if err != nil {
			reject(http.StatusBadRequest, err)
			return
		}
		if v != target.Locale {
			fields.Locale = Some(v)
			detail["locale"] = change{target.Locale, v}
			profileChanged = true
		}
	}
	// 8. profile preferences, stored as given
	if req.ProfilePreferences != nil {
		if v := strings.TrimSpace(*req.ProfilePreferences); v != target.ProfilePreferences {
			fields.ProfilePreferences = Some(v)
			detail["profile_preferences"] = change{target.ProfilePreferences, v}
			profileChanged = true
		}
	}
	// 9. subscription: persisted at once, like the status below.
	if req.SubscriptionTier != nil || req.SubscriptionExpiresAt != nil {
		if a.views.mode(ctx) != modePeriod || a.billing == nil {
			reject(http.StatusConflict, ErrPaymentRequired)
			return
		}
		subs, err := a.billing.Subscriptions(ctx, []uint{id})
		if err != nil {
			fail(c, err)
			return
		}
		cur, has := subs[id]
		curTier := "free"
		if has && strings.TrimSpace(cur.Tier) != "" {
			curTier = strings.ToLower(strings.TrimSpace(cur.Tier))
		}
		var curExpiry *time.Time
		if has {
			curExpiry = cur.ExpiresAt
		}
		tier, expiry := curTier, curExpiry
		if req.SubscriptionTier != nil {
			tier = strings.ToLower(strings.TrimSpace(*req.SubscriptionTier))
			if tier == "" {
				tier = "free"
			}
		}
		if req.SubscriptionExpiresAt != nil {
			t := req.SubscriptionExpiresAt.UTC()
			expiry = &t
		}
		if tier == "free" {
			expiry = nil
		}
		if tier != curTier || !sameInstant(expiry, curExpiry) {
			if err := a.billing.AssignPlan(ctx, id, tier, expiry); err != nil {
				fail(c, billingFailure(err))
				return
			}
			detail["subscription_tier"] = change{curTier, tier}
			detail["subscription_expires_at"] = change{curExpiry, expiry}
			otherChanged = true
		}
	}
	// 10. status
	statusRevoked := false
	if req.Status != nil {
		status := strings.TrimSpace(*req.Status)
		if !settableStatus(status) {
			reject(http.StatusBadRequest, ErrInvalidStatus)
			return
		}
		if status != target.Status {
			if acting.ID == target.ID {
				reject(http.StatusConflict, ErrSelfStatusChange)
				return
			}
			if target.Role == RoleSuperAdmin {
				reject(http.StatusConflict, ErrSuperadminStatus)
				return
			}
			if err := a.accounts.ApplyStatus(ctx, id, status); err != nil {
				fail(c, err)
				return
			}
			detail["status"] = change{target.Status, status}
			statusRevoked = status != StatusActive
			otherChanged = true
		}
	}

	if !profileChanged && !otherChanged {
		reject(http.StatusBadRequest, ErrEmptyPatch)
		return
	}
	if profileChanged {
		if _, err := a.accounts.Update(ctx, id, fields); err != nil {
			if errors.Is(err, ErrLastSuperadmin) {
				err = ErrLastSuperadminRole
			}
			fail(c, err)
			return
		}
	}
	if rolled {
		if err := a.accounts.RevokeAllSessions(ctx, id, "role changed to "+newRole); err != nil {
			fail(c, err)
			return
		}
	}
	if emailReset {
		detail["email_verification_reset"] = true
	}
	if phoneReset {
		detail["phone_verification_reset"] = true
	}
	if rolled || statusRevoked {
		detail["sessions_revoked"] = true
	}
	if r := strings.TrimSpace(req.Reason); r != "" {
		detail["reason"] = r
	}
	a.record(c, "admin_patch_user", id, flattenChanges(detail))
	a.respondUser(c, id)
}

// sameInstant compares two optional times by instant.
func sameInstant(x, y *time.Time) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	return x.Equal(*y)
}

// flattenChanges renders the audit detail of a patch the way it has always been
// stored: every changed field as a from_<name> and to_<name> pair and every
// other value (flags, reason) as text.
func flattenChanges(detail gin.H) gin.H {
	out := gin.H{}
	for k, v := range detail {
		if ch, ok := v.(change); ok {
			out["from_"+k] = changeText(ch.From)
			out["to_"+k] = changeText(ch.To)
			continue
		}
		out[k] = changeText(v)
	}
	return out
}

func changeText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case *time.Time:
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339Nano)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case string:
		return t
	default:
		return fmt.Sprint(v)
	}
}
