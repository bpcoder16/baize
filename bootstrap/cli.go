package bootstrap

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/bpcoder16/pixiu/logit"
)

// RunCLI 读取 -c 或 --conf 指定的配置（默认 ./conf/app.yaml），初始化并记录完成日志，回收后返回进程退出码。
// 宿主可在 main 中调用 os.Exit(bootstrap.RunCLI())。需要继续使用日志的宿主可以直接调用 Init。
func RunCLI() int {
	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	const defaultPath = "./conf/app.yaml"
	var path string
	flags.StringVar(&path, "conf", defaultPath, "应用配置文件路径")
	flags.StringVar(&path, "c", defaultPath, "--conf 的简写")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flags.Arg(0))
		flags.Usage()
		return 2
	}
	resources, err := Init(path)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	logit.Info(context.Background(), "白泽初始化完成")
	if err = resources.Close(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
