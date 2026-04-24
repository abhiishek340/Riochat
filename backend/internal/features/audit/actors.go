package audit

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
)

// Actor is the identity information shown next to a record.
type Actor struct {
	Username    string
	DisplayName string
}

// Label is the friendly name: display name, else username, else the numeric id.
func (a Actor) label(id uint) string {
	if n := strings.TrimSpace(a.DisplayName); n != "" {
		return n
	}
	if n := strings.TrimSpace(a.Username); n != "" {
		return n
	}
	return strconv.FormatUint(uint64(id), 10)
}

// ActorResolver looks up a user. found is false when the user no longer
// exists. The user area (or an adapter written by the integrator) provides the
// implementation; this area does not read the user tables itself.
type ActorResolver interface {
	ResolveActor(ctx context.Context, userID uint) (actor Actor, found bool, err error)
}

// ActorResolverFunc adapts a function to ActorResolver.
type ActorResolverFunc func(ctx context.Context, userID uint) (Actor, bool, error)

// ResolveActor calls f.
func (f ActorResolverFunc) ResolveActor(ctx context.Context, id uint) (Actor, bool, error) {
	return f(ctx, id)
}

var resolver atomic.Pointer[ActorResolver]

// SetActorResolver registers the user lookup used by the list endpoint. Until
// one is registered every actor name and label is empty. Last writer wins; nil
// clears it.
func SetActorResolver(r ActorResolver) {
	if r == nil {
		resolver.Store(nil)
		return
	}
	resolver.Store(&r)
}

func currentResolver() ActorResolver {
	if p := resolver.Load(); p != nil {
		return *p
	}
	return nil
}
