package appconfig_test

import (
	"fmt"
	"log"

	"github.com/bpcoder16/baize/appconfig"
)

func ExampleLoad() {
	// 示例从 appconfig 包目录运行。宿主部署后传入自己的 "./conf/app.yaml"。
	cfg, err := appconfig.Load("../conf.example/app.yaml")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.Env.AppName, cfg.Env.RunMode)
	// Output: example debug
}
