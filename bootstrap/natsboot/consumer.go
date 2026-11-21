package natsboot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/internal/instancekey"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/natsx"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ConsumerService 为已声明的 NATS 实例装配 nats-consumer:<实例名> 服务。
// Build 读取同目录的 nats-register.<实例名>.yaml 并绑定 Handler，Run 才注册订阅。
// 每个实例只声明一次。服务负责停收与排空，共享连接由 Instances 回收。
func ConsumerService(name string, js *natsx.JetStreamHandlerRegistry, core *natsx.CoreHandlerRegistry) bootstrap.Service {
	serviceName := constant.ServiceNATSConsumer + ":" + name
	return bootstrap.Service{
		Name: serviceName,
		Build: func(_ context.Context, dir string) (apprun.Service, error) {
			if !instancekey.Valid(name) {
				return apprun.Service{}, fmt.Errorf("natsboot: invalid NATS instance name %q", name)
			}
			path := filepath.Join(dir, "nats-register."+name+".yaml")
			cfg, err := configx.Parse[registrationConfig](path)
			if err != nil {
				return apprun.Service{}, err
			}
			client, err := lookupClient(name)
			if err != nil {
				return apprun.Service{}, err
			}
			// 副本数的默认上限取自实例配置的地址数，避免运行时发现的节点改变策略。
			batch, err := cfg.bind(len(client.Conn().Opts.Servers), js, core)
			if err != nil {
				return apprun.Service{}, fmt.Errorf("natsboot: bind subscriptions from %q: %w", path, err)
			}
			registerCtx, cancel := context.WithCancel(context.Background())
			c := &consumerService{
				client:             client,
				batch:              batch,
				path:               path,
				registerCtx:        registerCtx,
				cancelRegistration: cancel,
			}
			return apprun.Service{
				Name:   serviceName,
				Run:    c.run,
				Stop:   c.stop,
				Wait:   client.WaitConsumers,
				Cancel: c.cancel,
			}, nil
		},
	}
}

// Named 在实例缺失或已关闭时 panic，装配错误由 Build 返回给宿主。
func lookupClient(name string) (client *natsx.Client, err error) {
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("natsboot: look up NATS %q: %v", name, cause)
		}
	}()
	return natsx.Named(name), nil
}

type consumerService struct {
	client             *natsx.Client
	batch              *registrations
	path               string
	registerCtx        context.Context
	cancelRegistration context.CancelFunc
	started            atomic.Bool
}

func (c *consumerService) run() error {
	if c.registerCtx.Err() != nil {
		return nil
	}
	if !c.started.CompareAndSwap(false, true) {
		return apprun.ErrStarted
	}
	conn := c.client.Conn()
	// 先监听永久关闭再检查当前状态，覆盖注册期间断开的情况。临时断线由客户端重连。
	closed := conn.StatusChanged(nats.CLOSED)
	defer conn.RemoveStatusListener(closed)
	var failure error = nats.ErrConnectionClosed
	if !conn.IsClosed() {
		failed := make(chan error, 1)
		for i := range c.batch.jetStreamConsumers {
			consumer := &c.batch.jetStreamConsumers[i]
			consumer.Consume.ErrorHandler = func(_ jetstream.ConsumeContext, err error) {
				if errors.Is(err, jetstream.ErrNoHeartbeat) {
					return
				}
				// 多个拉取循环可能同时退出，只需将首个故障交给服务管理器。
				select {
				case failed <- fmt.Errorf("consumer %q/%q: %w", consumer.Stream, consumer.Config.Durable, err):
				default:
				}
			}
		}
		if err := c.register(); err != nil {
			if c.registerCtx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, natsx.ErrClosed) || errors.Is(err, nats.ErrConnectionClosed)) {
				return nil
			}
			return fmt.Errorf("natsboot: register from %q: %w", c.path, err)
		}
		select {
		case <-c.registerCtx.Done():
			return nil
		case <-closed:
		case failure = <-failed:
		}
	}
	if c.registerCtx.Err() != nil {
		return nil
	}
	return fmt.Errorf("natsboot: subscriptions from %q: %w", c.path, failure)
}

func (c *consumerService) register() error {
	if err := c.client.RegisterStreamRegistrations(c.registerCtx, c.batch.streams...); err != nil {
		return fmt.Errorf("register streams: %w", err)
	}
	if err := c.client.RegisterJetStreamConsumers(c.registerCtx, c.batch.jetStreamConsumers...); err != nil {
		return fmt.Errorf("register JetStream consumers: %w", err)
	}
	if err := c.client.RegisterCoreConsumers(c.registerCtx, c.batch.coreConsumers...); err != nil {
		return fmt.Errorf("register Core consumers: %w", err)
	}
	return nil
}

// 停收先中断注册，给在途 Handler 留出排空时间；超时后才取消消息上下文。
func (c *consumerService) stop() error {
	c.cancelRegistration()
	c.client.StopConsumers()
	return nil
}

func (c *consumerService) cancel() {
	c.cancelRegistration()
	c.client.CancelConsumers()
}
