package bootstrap_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bpcoder16/baize/bootstrap"
)

func TestRunCLIExitCodes(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	missing := filepath.Join(t.TempDir(), "missing.yaml")

	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"-h"}, 0},
		{[]string{"--conf", missing}, 1},
		{[]string{"-c", missing}, 1},
		{[]string{"-unknown"}, 2},
	} {
		os.Args = append([]string{"demo"}, tc.args...)
		if got := bootstrap.RunCLI(bootstrap.Plan{}); got != tc.want {
			t.Errorf("RunCLI(%v) = %d, want %d", tc.args, got, tc.want)
		}
	}
}
