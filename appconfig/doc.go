// Package appconfig 读取应用环境、日志配置和常驻服务禁用规则。
//
// # 单独读取配置
//
// 从白泽仓库的 conf.example/app.yaml 复制模板到宿主的 conf/。配置检查工具可调用 [Load]。
//
//	cfg, err := appconfig.Load("./conf/app.yaml")
//	if err != nil {
//		return err
//	}
//	fmt.Println(cfg.Env.AppName, cfg.Env.ConfigDirPath)
//
// [Load] 支持 YAML、TOML、JSON，按文件扩展名严格解码。未知字段、错误类型和非法禁用规则
// 都会返回错误。每次成功调用都会返回一份独立的 [Config]。
//
// # 从读取到初始化
//
// 命令行宿主调用 os.Exit(bootstrap.RunCLI(plan))，已有 context 的宿主调用 bootstrap.Run(ctx, path, plan)。
// bootstrap 来自 github.com/bpcoder16/baize/bootstrap。Run 发布 Pixiu 环境并应用时区，
// 然后预检日志参数并打开日志，按清单执行准备步骤和服务，返回前关闭资源栈。
// serviceShutdownTimeout 控制 Run 中全部服务停收和排空的总期限，省略或写 0s 使用 30 秒。
// resourceCloseTimeout 控制资源栈回收的总期限。省略或写 0s 使用 30 秒，负值在加载时返回错误。
// 只含 env 段的文件可单独解码，初始化还需要 env.appName、env.runMode、env.timeLocation 和 log.format。
//
// # 路径与配置兼容
//
// 入口文件相对路径以工作目录为基准。Env.ConfigDirPath 取入口文件的绝对父目录，符号链接取链接所在目录。
// log.dir 的相对路径以进程启动工作目录为基准。例如，在 /srv/shop 加载 /etc/shop/app.yaml，
// log.dir 为 ./log、应用名为 shop 时，默认 Info 日志位于 /srv/shop/log/shop/biz/info.log。
//
// disabledServices 由 Run 在服务构造前按类型或完整名称过滤。可用类型由
// github.com/bpcoder16/baize/constant 中的 IsServiceType 白名单定义。
package appconfig
