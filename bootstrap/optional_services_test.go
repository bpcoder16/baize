package bootstrap_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestOptionalServiceDependencies(t *testing.T) {
	for _, pkg := range []string{".", "./httpboot"} {
		out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatal(err)
		}
		deps := strings.Fields(string(out))
		if slices.Contains(deps, "github.com/gin-gonic/gin") != (pkg == "./httpboot") {
			t.Errorf("%s 的 Gin 依赖与服务声明不符", pkg)
		}
		for _, dep := range []string{
			"github.com/bpcoder16/pixiu/biz/bootstrap",
			"github.com/bpcoder16/pixiu/infra/natsx",
			"github.com/bpcoder16/pixiu/infra/websocketx",
			"github.com/bpcoder16/pixiu/infra/cronx",
		} {
			if slices.Contains(deps, dep) {
				t.Errorf("%s 引入了无关服务依赖 %s", pkg, dep)
			}
		}
	}
}
