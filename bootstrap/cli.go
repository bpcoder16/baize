package bootstrap

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// RunCLI 解析 -c / --conf，接管 SIGINT、SIGTERM 并运行 plan。
// 正常退出或显示帮助返回 0，启动、运行或关闭失败返回 1，参数错误返回 2。
func RunCLI(plan Plan) int {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 首次信号触发排空，同时恢复系统信号处理，允许再次中断。
	context.AfterFunc(ctx, stop)
	if err := Run(ctx, path, plan); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
