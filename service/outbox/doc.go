// Package outbox 提供 JetStream 事务发件箱的业务入口。任务和业务写入在同一事务登记。
// 提交后可以主动发送，可重试的失败由启动时装配的 Cron 补偿。
//
// 在 Plan.Prepare 中通过 outboxboot.PrepareJetStream 配置主库、有序发布资源、CronTaskEvery 和 CronTaskTimeout。
// 启动时借用已声明的主库与 JetStream 实例，准备表并注册补偿任务。
//
//	var taskID int64
//	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
//		if err := tx.Create(&order).Error; err != nil {
//			return err
//		}
//		var err error
//		taskID, err = outbox.EnqueueJetStreamTx(tx, outbox.JetStreamMessage{
//			ResourceName: "events",
//			Subject:      "orders.created",
//			Data:         payload,
//			Header:       map[string][]string{},
//			ExpiresAt:    time.Now().Add(24 * time.Hour),
//		})
//		return err
//	})
//	if err != nil {
//		return err
//	}
//	outbox.SendAsyncJetStream(taskID)
//	return nil
//
// 业务示例使用 service/outbox、gorm.io/gorm 和 time。
// ctx、db、order、payload 由应用提供。events 指向已声明的 JetStream 实例。
// db 须来自所选主库的同一个连接池，事务中使用配置的数据库和 schema。
// 示例中的 db.Transaction 是最外层事务。提交后的发送失败由补偿流程处理。
//
// 每种 Handler 类型在进程内最多登记一个 Worker。PrepareJetStream 登记 nats_jetstream 类型。
// 自定义类型为 1 至 32 字节的 ASCII 字符串，须避开零字节和尾随空格。
// 包级 Send(ctx, handlerType, taskID) 和 SendAsync(handlerType, taskID) 按显式类型选择 Worker。
// 指定类型未登记时，入队与同步发送返回 ErrHandlerNotRegistered，异步发送在调用方 panic。
//
// SendAsyncJetStream(taskID) 在调用方检查 JetStream Worker 后启动 goroutine。
// 每次发送从 context.Background() 派生独立的 5 秒超时 context，不携带请求 context 的值，
// Handler 和发送日志从任务记录恢复 logId。异步入口不返回错误，失败额外记录 OutboxAsyncFailed；
// pending 任务由 cron 补偿，不使用 taskpool、不排队、不限制并发，也不在内部重试。
// Handler 须响应这次尝试的取消信号。
// 如需同步等待结果，使用 outbox.SendJetStream(ctx, taskID)。发送失败不能撤销已提交业务。
//
// baize.outbox.retry.nats_jetstream 按 CronTaskEvery 调度，整轮使用 CronTaskTimeout，每页 200 条。
// Worker.RetryPending(ctx, batchSize) 按绑定的类型确定扫描上界、分页、持锁复核和清理范围。
// 按 ID 游标扫描到固定上界，每条任务在一次扫描中最多尝试一次。
// 每条补偿尝试有 5 秒期限，也受整轮 context 的更早取消控制。单条超时后继续扫描后续任务。
// 整轮取消后，同一 Worker 下次从最后尝试的 ID 之后继续。扫完上界后再从头重试并纳入新任务。
// 扫描进度保存在各 Worker 内存中，重启后从头扫描。
// 补偿轮次由 cronx 分布式锁互斥，本地不加锁；失租重叠时数据库行锁仍保护单条任务。
// 发送成功记为 succeeded。目标缺失或不在白名单时，首次失败即记为 failed，并保留 LastError。
// 已过期的任务也进入 failed。临时发送失败保留 pending，等待下轮补偿。
// 补偿后按当前时间判断 Store 当地 [02:00, 04:00) 窗口，窗口内清理历史，即使没有 pending 也执行。
// succeeded 和 failed 按 created_at 保留 30 天，只删除创建时间早于清理阶段开始时刻减去 30×24 小时的记录。
// 单批最多清理 200 条，每批前检查当前时间。锁定的行留待后续轮次。
// 扫描错误及单条超时以外的存储错误中止本轮。清理错误记 OutboxCleanupFailed。Send 接到已清理的任务 ID 时返回 ErrNotFound。
// JetStream 入队要求合法 Subject、显式的 Data 和 Header（可为空）及非零 ExpiresAt。Header 名须符合 HTTP token 规则。
// ResourceName 省略时取本次清单的首项，实际名称与 nats_jetstream 类型在事务中一同持久化。
// logId 是可选调试字段。有效 UTF-8、不含零字节且至多 65535 字节的值原样入库，其余留空。
// 主动发送与补偿发送均使用 FOR UPDATE NOWAIT；不同任务可并发发送。
// 同一任务被锁时，Send 返回 ErrBusy，补偿跳过该任务。cronx 分布式锁协调补偿轮次。
// 每次尝试的加锁、发布和回写共用同一个 context。提交前取消时，数据库回滚并释放行锁。
// 持锁读取后按应用服务器时间判断过期。尝试次数、失败原因和状态一起提交，回滚时保留上次提交的值。
// 已进入 MySQL 驱动的提交和回滚还受连接读写超时控制。
// 数据库生成 int64 自增 ID，重复登记会生成新任务；重试沿用 ID，回写失败可能重发，消费者须幂等。
// Prepare 通过 repository/outbox.Open 迁移 baize_outbox。同库同 schema 的 Worker 可以共用该表。
// 同表同类型的副本共享完整的资源清单。新增目标先在所有投递副本生效，再向它登记任务。
// 移除目标先停写，等待在途业务事务结束且该目标的 pending 清空，再从清单移除。
// 主动发送传错类型时返回 ErrHandlerTypeMismatch，任务保持原状。
//
// Location 省略时为 UTC，两种数据库均保留绝对时刻，读取按配置时区表示。
// MySQL 时间列为 TIMESTAMP(6)，驱动须开启 parseTime，且驱动 Location 与会话 time_zone 一致；
// 推荐连接使用 UTC，展示时区可独立传入 env.TimeLocation()。PostgreSQL 使用 TIMESTAMPTZ(6)。
// 迁移显式声明创建/更新时间的默认值和期限/完成时间的 NULL，兼容关闭 explicit_defaults_for_timestamp 的严格模式。
// 更新时间仍由 Store 显式写入，时间列不设置 ON UPDATE；无需调整数据库实例参数。
// MySQL 非空 ExpiresAt 的 UTC 整秒范围为 1 至 2147483647，越界返回 ErrInvalidArgument。
// 退出时先停止业务/cron，并等待同步发送及补偿调用返回，再关闭 MQ/数据库，最后关闭日志。
// SendAsync 不提供等待或排空，退出可能中断本次尝试，pending 任务由后续 cron 补偿；
// 必须在关闭底层资源前确认发送结束时，应使用 Send 并由应用等待同步调用完成。
package outbox
