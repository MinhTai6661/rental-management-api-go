package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsBusy(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"statement timeout": {&pgconn.PgError{Code: "57014"}, true},
		"lock timeout":      {&pgconn.PgError{Code: "55P03"}, true},
		"wrapped":           {fmt.Errorf("query: %w", &pgconn.PgError{Code: "57014"}), true},
		"bad text":          {&pgconn.PgError{Code: "22P02"}, false},
		"plain error":       {errors.New("boom"), false},
		"nil":               {nil, false},
	}
	for name, tc := range cases {
		if got := IsBusy(tc.err); got != tc.want {
			t.Errorf("%s: IsBusy = %v, want %v", name, got, tc.want)
		}
	}
}

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(fmt.Errorf("load: %w", pgx.ErrNoRows)) || IsNotFound(errors.New("x")) {
		t.Error("IsNotFound must match pgx.ErrNoRows only")
	}
}
