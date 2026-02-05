package user

import (
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Errors of the administrator-facing account surface. The protection codes
// carry their own public texts, which differ from the self-service wording
// other areas use for some of the same codes.
var (
	ErrAdminOnly = apperr.ErrAdminRequired.WithStatus(http.StatusForbidden)

	ErrInvalidStatus = def(http.StatusBadRequest, "user.invalid_status", "invalid user status")
	ErrInvalidRole   = def(http.StatusBadRequest, "user.invalid_role", "invalid user role")
	ErrEmptyPatch    = def(http.StatusBadRequest, "user.empty_patch", "at least one user field is required")

	ErrSuperadminManagement = def(http.StatusForbidden, "user.superadmin_management_protected", "superadmin management is not allowed")

	ErrSuperadminStatus     = def(http.StatusConflict, "user.superadmin_status_protected", "superadmin status change is not allowed")
	ErrLastSuperadminRole   = def(http.StatusConflict, "user.last_superadmin_role_protected", "last superadmin role change is not allowed")
	ErrSelfRoleChange       = def(http.StatusConflict, "user.self_role_change_not_allowed", "self role change is not allowed")
	ErrSelfStatusChange     = def(http.StatusConflict, "user.self_status_change_not_allowed", "self status change is not allowed")
	ErrSuperadminPassword   = def(http.StatusConflict, "user.superadmin_password_reset_protected", "superadmin password reset is not allowed")
	ErrSuperadminTwoFactor  = def(http.StatusConflict, "user.superadmin_two_factor_reset_protected", "superadmin two factor reset is not allowed")
	ErrSuperadminDeleteConf = def(http.StatusConflict, "user.superadmin_delete_protected", "superadmin account deletion is not allowed")
	ErrSelfDelete           = def(http.StatusConflict, "user.self_delete_not_allowed", "self delete is not allowed")

	// ErrPaymentRequired answers a subscription edit while billing is not in
	// period mode.
	ErrPaymentRequired = def(http.StatusConflict, "billing.payment_required", "payment is required")

	ErrInvalidImportDSN        = def(http.StatusBadRequest, "request.invalid_import_dsn", "invalid import dsn")
	ErrInvalidImportMultiplier = def(http.StatusBadRequest, "request.invalid_import_credit_multiplier", "invalid import credit multiplier")
)
