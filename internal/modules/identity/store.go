package identity

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/internal/modules/identity/sqlcgen"
	"rental-management/internal/platform/db"
)

// Store gives the service its queries, plain or inside one transaction.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

// NewStore returns a store on the pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: sqlcgen.New(pool)} }

// InTx runs fn with queries bound to one transaction: it commits when fn returns nil, rolls back otherwise.
func (s *Store) InTx(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error { return fn(s.q.WithTx(tx)) })
}

// permissionCache keeps the permission set of each role in memory for a short time, so a request does not read
// the role_permissions table. A role's permissions change only when the catalog is seeded, so a minute of delay is fine.
type permissionCache struct {
	q   *sqlcgen.Queries
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[string]cachedPermissions
}

type cachedPermissions struct {
	set     map[string]struct{}
	expires time.Time
}

func newPermissionCache(q *sqlcgen.Queries, ttl time.Duration, now func() time.Time) *permissionCache {
	return &permissionCache{q: q, ttl: ttl, now: now, entries: map[string]cachedPermissions{}}
}

// ForRole returns the permissions of the role. The returned set is shared: callers must not modify it.
func (c *permissionCache) ForRole(ctx context.Context, role string) (map[string]struct{}, error) {
	now := c.now()
	c.mu.Lock()
	entry, ok := c.entries[role]
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.set, nil
	}

	codes, err := c.q.ListPermissionCodesOfRole(ctx, role)
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

// sortedKeys lists a permission set in a stable order for the API.
func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
