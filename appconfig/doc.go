// Package appconfig 读取应用环境、日志配置和常驻服务禁用规则。
//
// # 单独读取配置
//
// 从白泽仓库的 conf.example/app.yaml 复制模板到宿主的 conf/。配置检查工具可单独调用 [Load]。
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
// 命令行宿主可以用 os.Exit(bootstrap.RunCLI()) 初始化并退出。继续工作的宿主可调用
// bootstrap.Init(path)，成功后在进程退出时关闭返回的资源栈。bootstrap 来自
// github.com/bpcoder16/baize/bootstrap。Init 发布 Pixiu 环境并应用时区，然后预检日志参数并打开日志。
// 只含 env 段的文件可单独 Load，初始化还需要 env.appName、env.runMode、env.timeLocation 和 log.format。
//
// # 路径与配置兼容
//
// 入口文件相对路径以工作目录为基准。Env.ConfigDirPath 取入口文件的绝对父目录，符号链接取链接所在目录。
// log.dir 的相对路径以进程启动工作目录为基准。例如，在 /srv/shop 加载 /etc/shop/app.yaml，
// log.dir 为 ./log、应用名为 shop 时，默认 Info 日志位于 /srv/shop/log/shop/biz/info.log。
//
// disabledServices 兼容已有配置，目前只校验类型与实例名语法。四种类型的公开常量位于
// github.com/bpcoder16/baize/constant。初始化不受服务开关影响。
package appconfig
