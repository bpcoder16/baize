package db

import (
	"testing"

	"github.com/bpcoder16/baize/constant"
)

func TestLookupClient(t *testing.T) {
	for _, tc := range []struct {
		backend constant.Backend
		want    string
	}{
		{constant.MySQL, `mysql instance "orders" is not declared`},
		{constant.PostgreSQL, `postgresql instance "orders" is not declared`},
		{"sqlite", `unsupported database backend "sqlite"`},
	} {
		t.Run(string(tc.backend), func(t *testing.T) {
			client, err := LookupClient(t.Context(), tc.backend, "orders")
			if client != nil || err == nil || err.Error() != tc.want {
				t.Fatalf("LookupClient error = %v, want %q", err, tc.want)
			}
		})
	}
}
