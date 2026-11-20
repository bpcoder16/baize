package bootstrap_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestNamedInstances(t *testing.T) {
	type settings struct {
		Value string `mapstructure:"value"`
	}
	openErr := errors.New("open failed")
	for _, tc := range []struct {
		label      string
		names      []string
		wantErr    string
		wantOpened []string
	}{
		{label: "ordered", names: []string{"west", "east"}, wantOpened: []string{"west:one", "east:two"}},
		{label: "later open fails", names: []string{"west", "bad"}, wantErr: "bad", wantOpened: []string{"west:one", "bad:fail"}},
		{label: "later file missing", names: []string{"west", "missing"}, wantErr: "sample.missing.yaml"},
		{label: "duplicate name", names: []string{"west", "west"}, wantErr: "west"},
		{label: "invalid name", names: []string{"../west"}, wantErr: "../west"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			dir := t.TempDir()
			for name, value := range map[string]string{"west": "one", "east": "two", "bad": "fail"} {
				if err := os.WriteFile(filepath.Join(dir, "sample."+name+".yaml"), []byte("value: "+value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var opened []string
			closed := 0
			step := bootstrap.NamedInstances("sample", tc.names, func(name string, cfg *settings) error {
				opened = append(opened, name+":"+cfg.Value)
				if cfg.Value == "fail" {
					return openErr
				}
				return nil
			}, func() error {
				closed++
				return nil
			})
			stack := new(lifecycle.Stack)
			err := step.Init(dir, stack)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Init error = %v, want %q", err, tc.wantErr)
			}
			if !slices.Equal(opened, tc.wantOpened) {
				t.Fatalf("opened %v, want %v", opened, tc.wantOpened)
			}
			if err := stack.Close(); err != nil {
				t.Fatal(err)
			}
			if len(opened) > 0 && closed != 1 || len(opened) == 0 && closed != 0 {
				t.Fatalf("opened %v, closed %d times", opened, closed)
			}
		})
	}
}
