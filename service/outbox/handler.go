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

// HandlerConfig 把一个发布器绑定到其有序的目标实例清单。首项为默认目标。
type HandlerConfig struct {
	Handler       Handler
	ResourceNames []string
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
