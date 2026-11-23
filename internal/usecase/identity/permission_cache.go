package identity

import (
	"context"
	"sort"
	"sync"
	"time"
)

// permissionCache keeps the permission set of each role in memory for a short time, so a request does not read
// the role_permissions table. A role's permissions change only when the catalog is seeded, so a minute of delay is fine.
type permissionCache struct {
	repo  PermissionRepository
	ttl   time.Duration
	clock Clock

	mu      sync.Mutex
	entries map[string]cachedPermissions
}

type cachedPermissions struct {
	set     map[string]struct{}
	expires time.Time
}

func newPermissionCache(repo PermissionRepository, ttl time.Duration, clock Clock) *permissionCache {
	return &permissionCache{repo: repo, ttl: ttl, clock: clock, entries: map[string]cachedPermissions{}}
}

// ForRole returns the permissions of the role. The returned set is shared: callers must not modify it.
func (c *permissionCache) ForRole(ctx context.Context, role string) (map[string]struct{}, error) {
	now := c.clock.Now()
	c.mu.Lock()
	entry, ok := c.entries[role]
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.set, nil
	}

	codes, err := c.repo.CodesOfRole(ctx, role)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		set[code] = struct{}{}
	}
	c.mu.Lock()
	c.entries[role] = cachedPermissions{set: set, expires: now.Add(c.ttl)}
	c.mu.Unlock()
	return set, nil
}

// sortedPermissionCodes lists a permission set in a stable order for the API.
func sortedPermissionCodes(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
