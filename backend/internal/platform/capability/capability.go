// Package capability models the append-only set of server feature flags that
// clients and the router use to decide which route groups exist.
package capability

import (
	"fmt"
	"sort"
)

// Flag names. The list is append-only; the key strings are an API contract.
const (
	MultiUser         = "multiUser"
	Registration      = "registration"
	IdentityProviders = "identityProviders"
	AccountSecurity   = "accountSecurity"
	Announcements     = "announcements"
	BillingGating     = "billingGating"
	UsageMetering     = "usageMetering"
	ContentModeration = "contentModeration"
	Sharing           = "sharing"
	DesktopDownload   = "desktopDownload"
)

// Names lists every known flag in a stable order.
func Names() []string {
	return []string{
		MultiUser, Registration, IdentityProviders, AccountSecurity, Announcements,
		BillingGating, UsageMetering, ContentModeration, Sharing, DesktopDownload,
	}
}

// Known reports whether name is a defined flag.
func Known(name string) bool {
	for _, n := range Names() {
		if n == name {
			return true
		}
	}
	return false
}

// MustBeKnown panics when name is not a defined flag. Route registration calls it
// so a misspelled flag aborts startup instead of silently disabling a group.
func MustBeKnown(name string) {
	if !Known(name) {
		panic(fmt.Sprintf("capability: unknown flag %q", name))
	}
}

// Set is an immutable-by-convention map of flag values.
type Set map[string]bool

// Server returns the flags of a regular server deployment: everything on.
func Server() Set {
	s := Set{}
	for _, n := range Names() {
		s[n] = true
	}
	return s
}

// Local returns the flags of the single-user desktop sidecar: only usage
// metering is on.
func Local() Set {
	s := Set{}
	for _, n := range Names() {
		s[n] = false
	}
	s[UsageMetering] = true
	return s
}

// Enabled reports a flag's value; unknown names are always false.
func (s Set) Enabled(name string) bool {
	if !Known(name) {
		return false
	}
	return s[name]
}

// Features returns a copy suitable for the public capabilities payload.
func (s Set) Features() map[string]bool {
	out := make(map[string]bool, len(Names()))
	for _, n := range Names() {
		out[n] = s[n]
	}
	return out
}

// Provider supplies flags at request time so configuration changes apply
// immediately.
type Provider interface {
	Enabled(name string) bool
	Features() map[string]bool
}

// Sorted returns the names of enabled flags (diagnostics, tests).
func (s Set) Sorted() []string {
	var out []string
	for n, v := range s {
		if v {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
