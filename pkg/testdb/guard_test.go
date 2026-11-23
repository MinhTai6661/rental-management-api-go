package testdb

import "testing"

func TestGuardAcceptsOnlyTheTestContainer(t *testing.T) {
	const want = "host.docker.internal:49321"
	cases := []struct {
		name     string
		host     string
		port     uint16
		user     string
		database string
		ok       bool
	}{
		{"admin database", "host.docker.internal", 49321, "rental_management_test", "postgres", true},
		{"template", "host.docker.internal", 49321, "rental_management_test", "rental_management_tmpl", true},
		{"clone", "host.docker.internal", 49321, "rental_management_test", "rental_management_test_ab12", true},
		{"go dev database port", "localhost", 5442, "rental_management_test", "rental_management_test_x", false},
		{"laravel dev database port", "localhost", 5432, "rental_management_test", "rental_management_test_x", false},
		{"other host with the same port", "localhost", 49321, "rental_management_test", "rental_management_test_x", false},
		{"wrong user", "host.docker.internal", 49321, "root", "rental_management_test_x", false},
		{"dev database name", "host.docker.internal", 49321, "rental_management_test", "rental_management_dev", false},
		{"laravel database name", "host.docker.internal", 49321, "rental_management_test", "rental_management_2", false},
	}
	for _, c := range cases {
		err := CheckPostgresTarget(c.host, c.port, c.user, c.database, want)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
		}
	}
	if err := CheckPostgresTarget("h", 1, "rental_management_test", "postgres", ""); err == nil {
		t.Error("with no container running every target must be refused")
	}
}

func TestGuardForRedis(t *testing.T) {
	if err := CheckRedisTarget("host.docker.internal", 49500, "host.docker.internal:49500"); err != nil {
		t.Errorf("the test container must be accepted: %v", err)
	}
	for _, port := range []uint16{6379, 6389} {
		if err := CheckRedisTarget("localhost", port, "localhost:6379"); err == nil {
			t.Errorf("port %d (a development Redis) must be refused", port)
		}
	}
	if err := CheckRedisTarget("localhost", 49501, "localhost:49500"); err == nil {
		t.Error("another address must be refused")
	}
}
