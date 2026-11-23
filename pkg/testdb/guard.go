// Package testdb starts throwaway Postgres and Redis containers for the integration tests (build tag
// "integration"). Tests never read DATABASE_URL or REDIS_URL and never touch a database that was not created
// here: every connection goes through a guard that refuses anything but the containers' own endpoints.
//
// This file (no build tag) holds the guard so it is unit tested with the rest.
package testdb

import (
	"fmt"
	"strings"
)

const (
	// Label marks every container the tests start, so leftovers are easy to find and remove.
	Label = "rental-management.test"

	postgresUser      = "rental_management_test"
	templateDatabase  = "rental_management_tmpl"
	cloneDatabasePref = "rental_management_test_"
	adminDatabase     = "postgres"
)

// forbiddenPorts are the ports of the development databases (the Go stack and the old Laravel stack). A test
// that would connect to one of them is a bug, so the guard stops it.
func forbiddenPorts() []uint16 { return []uint16{5432, 5442, 6379, 6389} }

// CheckPostgresTarget returns an error unless the connection goes to the test container (wantHostPort is the
// "host:port" the container is published on), as the test user, to one of the databases the harness creates.
func CheckPostgresTarget(host string, port uint16, user, database, wantHostPort string) error {
	if err := checkEndpoint(host, port, wantHostPort); err != nil {
		return err
	}
	if user != postgresUser {
		return fmt.Errorf("testdb: refusing to connect as user %q (tests use %q)", user, postgresUser)
	}
	if database != adminDatabase && database != templateDatabase && !strings.HasPrefix(database, cloneDatabasePref) {
		return fmt.Errorf("testdb: refusing to use database %q (tests use %q, %q or %q*)",
			database, adminDatabase, templateDatabase, cloneDatabasePref)
	}
	return nil
}

// CheckRedisTarget returns an error unless the address is the test container's.
func CheckRedisTarget(host string, port uint16, wantHostPort string) error {
	return checkEndpoint(host, port, wantHostPort)
}

func checkEndpoint(host string, port uint16, wantHostPort string) error {
	if wantHostPort == "" {
		return fmt.Errorf("testdb: no test container is running, refusing to connect to %s:%d", host, port)
	}
	for _, p := range forbiddenPorts() {
		if port == p {
			return fmt.Errorf("testdb: port %d belongs to a development database, refusing to connect", port)
		}
	}
	if got := fmt.Sprintf("%s:%d", host, port); got != wantHostPort {
		return fmt.Errorf("testdb: %s is not the test container (%s), refusing to connect", got, wantHostPort)
	}
	return nil
}
