package outbox

import (
	"context"
	"fmt"
	"runtime/debug"

	model "github.com/bpcoder16/baize/model/outbox"
	"github.com/bpcoder16/pixiu/logit"
)

// Handler 同步发布一次，返回 nil 表示外部系统已确认。
type Handler interface {
	Publish(context.Context, model.Delivery) error
}

// JetStream 登记时要求发布器能从自己的客户端映射解析入队目标。
type resourceResolver interface {
	ResolveResource(name string) (string, error)
}

func publish(ctx context.Context, handler Handler, delivery model.Delivery) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("outbox: handler panic: %v", recovered)
			logit.Error(ctx, "OutboxPanic", logit.Int64("task_id", delivery.ID), logit.Str("stack", string(debug.Stack())))
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	return handler.Publish(ctx, delivery)
}
