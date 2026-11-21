package bootstrap_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestOptionalServiceDependencies(t *testing.T) {
	for _, pkg := range []string{".", "./httpboot", "./cronboot", "./natsboot", "./realtimeboot"} {
		out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatal(err)
		}
		deps := strings.Fields(string(out))
		if slices.Contains(deps, "github.com/gin-gonic/gin") != (pkg == "./httpboot") {
			t.Errorf("%s 的 Gin 依赖与服务声明不符", pkg)
		}
		if slices.Contains(deps, "github.com/bpcoder16/pixiu/infra/cronx") != (pkg == "./cronboot") {
			t.Errorf("%s 的 Cron 依赖与服务声明不符", pkg)
		}
		if slices.Contains(deps, "github.com/bpcoder16/pixiu/infra/natsx") != (pkg == "./natsboot") {
			t.Errorf("%s 的 NATS 依赖与服务声明不符", pkg)
		}
		if slices.Contains(deps, "github.com/bpcoder16/pixiu/biz/bootstrap") {
			t.Errorf("%s 引入了 Pixiu 的业务装配包", pkg)
		}
		for _, dep := range []string{
			"github.com/bpcoder16/baize/service/realtime",
			"github.com/bpcoder16/pixiu/infra/websocketx",
		} {
			if slices.Contains(deps, dep) != (pkg == "./realtimeboot") {
				t.Errorf("%s 的 Realtime 依赖 %s 与服务声明不符", pkg, dep)
			}
		}
	}
}
