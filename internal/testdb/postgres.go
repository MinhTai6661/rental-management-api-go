//go:build integration

package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"rental-management/internal/db"
)

// Postgres is a throwaway Postgres container with a migrated template database. Each test clones the template
// into a database of its own (fast), so tests never see each other's rows and never share state.
type Postgres struct {
	container *postgres.PostgresContainer
	host      string
	port      uint16
	password  string
}

// StartPostgres starts the container and prepares the template database (migrations applied). Call Terminate
// when the package's tests are done.
func StartPostgres(ctx context.Context) (*Postgres, error) {
	password, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	ctr, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase(adminDatabase),
		postgres.WithUsername(postgresUser),
		postgres.WithPassword(password),
		testcontainers.WithLabels(map[string]string{Label: "1"}),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres container: %w", err)
	}
	p := &Postgres{container: ctr, password: password}

	host, err := ctr.Host(ctx)
	if err != nil {
		p.Terminate()
		return nil, err
	}
	mapped, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		p.Terminate()
		return nil, err
	}
	port, err := strconv.ParseUint(mapped.Port(), 10, 16)
	if err != nil {
		p.Terminate()
		return nil, err
	}
	p.host, p.port = host, uint16(port)

	if err := p.prepareTemplate(ctx); err != nil {
		p.Terminate()
		return nil, err
	}
	return p, nil
}

// Terminate stops and removes the container.
func (p *Postgres) Terminate() { _ = testcontainers.TerminateContainer(p.container) }

// dsn builds the connection string of a database and checks it against the guard.
func (p *Postgres) dsn(database string) string {
	if err := CheckPostgresTarget(p.host, p.port, postgresUser, database, fmt.Sprintf("%s:%d", p.host, p.port)); err != nil {
		panic(err)
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", postgresUser, p.password, p.host, p.port, database)
}

func (p *Postgres) admin(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, p.dsn(adminDatabase))
}

func (p *Postgres) pool(ctx context.Context, database string) (*pgxpool.Pool, error) {
	return db.Open(ctx, db.Config{
		URL: p.dsn(database), MaxConns: 5, StatementTimeoutMS: 30000, LockTimeoutMS: 10000, IdleInTransactionTimeoutMS: 60000,
	})
}

func (p *Postgres) prepareTemplate(ctx context.Context) error {
	conn, err := p.admin(ctx)
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, "CREATE DATABASE "+templateDatabase)
	_ = conn.Close(ctx)
	if err != nil {
		return fmt.Errorf("create template database: %w", err)
	}

	pool, err := p.pool(ctx, templateDatabase)
	if err != nil {
		return err
	}
	// The template must have no open connection when it is cloned, so the pool is closed right after migrating.
	defer pool.Close()
	return db.Migrate(ctx, pool, "up", io.Discard)
}

// NewDB returns a pool on a fresh database cloned from the migrated template. The database is dropped when
// the test ends.
func (p *Postgres) NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return p.create(t, "TEMPLATE "+templateDatabase)
}

// NewEmptyDB returns a pool on a fresh database with no tables, for tests of the migrations themselves.
func (p *Postgres) NewEmptyDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return p.create(t, "")
}

func (p *Postgres) create(t *testing.T, options string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	suffix, err := randomHex(6)
	if err != nil {
		t.Fatal(err)
	}
	name := cloneDatabasePref + suffix

	conn, err := p.admin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name+" "+options); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}

	pool, err := p.pool(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin, err := p.admin(ctx)
		if err != nil {
			t.Logf("drop %s: %v", name, err)
			return
		}
		defer func() { _ = admin.Close(ctx) }()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})
	return pool
}

func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("random bytes: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
