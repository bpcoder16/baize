// Package httpboot 为宿主装配受白泽管理的 Gin HTTP 服务。
package httpboot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/env"
	"github.com/bpcoder16/pixiu/infra/ginx"
	"github.com/bpcoder16/pixiu/infra/httpserver"
	"github.com/gin-gonic/gin"
)

// 配置文件只接收可序列化的 Gin 选项，宿主路由在回调中注册。
type ginConfig struct {
	TrustedProxies   []string        `mapstructure:"trustedProxies"`
	LoggerName       string          `mapstructure:"loggerName"`
	RequireLogID     bool            `mapstructure:"requireLogID"`
	DisableAccessLog bool            `mapstructure:"disableAccessLog"`
	LogRequestInfo   bool            `mapstructure:"logRequestInfo"`
	LogResponseInfo  bool            `mapstructure:"logResponseInfo"`
	CORS             []ginx.CORSRule `mapstructure:"cors"`
}

// Gin 声明名为 http 的服务。Build 在启动阶段串行设置 Gin 模式，从入口配置同目录
// 读取 gin.yaml 和 http.yaml，并调用 register 注册业务路由。register 可以为 nil。
// debug/test 提供已注册的 Swag 文档，模块顺序决定导航顺序，省略时使用默认文档。
// release 仅提供业务路由。监听从 Run 开始，排空由白泽的服务管理器统一负责。
func Gin(register func(*gin.Engine), modules ...ginx.OpenAPIModule) bootstrap.Service {
	modules = slices.Clone(modules)
	return bootstrap.Service{
		Name: constant.ServiceHTTP,
		Build: func(_ context.Context, dir string) (apprun.Service, error) {
			return build(dir, register, modules)
		},
	}
}

func build(dir string, register func(*gin.Engine), modules []ginx.OpenAPIModule) (apprun.Service, error) {
	ginPath := filepath.Join(dir, "gin.yaml")
	cfg, err := configx.Parse[ginConfig](ginPath)
	if err != nil {
		return apprun.Service{}, err
	}
	httpPath := filepath.Join(dir, "http.yaml")
	httpCfg, err := configx.Parse[httpserver.Config](httpPath)
	if err != nil {
		return apprun.Service{}, err
	}

	mode := env.RunMode()
	gin.SetMode(mode)
	engineConfig := ginx.Config{
		TrustedProxies:   cfg.TrustedProxies,
		LoggerName:       cfg.LoggerName,
		RequireLogID:     cfg.RequireLogID,
		DisableAccessLog: cfg.DisableAccessLog,
		LogRequestInfo:   cfg.LogRequestInfo,
		LogResponseInfo:  cfg.LogResponseInfo,
	}
	if len(cfg.CORS) > 0 {
		middleware, corsErr := ginx.NewCORS(cfg.CORS...)
		if corsErr != nil {
			return apprun.Service{}, fmt.Errorf("httpboot: configure CORS from %q: %w", ginPath, corsErr)
		}
		engineConfig.Middlewares = []gin.HandlerFunc{middleware}
	}
	engine, err := ginx.New(engineConfig)
	if err != nil {
		return apprun.Service{}, fmt.Errorf("httpboot: create Gin engine from %q: %w", ginPath, err)
	}
	if mode != env.RunModeRelease {
		if err = ginx.RegisterOpenAPI(&engine.RouterGroup, modules...); err != nil {
			return apprun.Service{}, fmt.Errorf("httpboot: register OpenAPI (generate and import the Swag documentation): %w", err)
		}
	}
	server, err := httpserver.New(*httpCfg, engine)
	if err != nil {
		return apprun.Service{}, fmt.Errorf("httpboot: create HTTP server from %q: %w", httpPath, err)
	}
	if register != nil {
		register(engine)
	}
	return apprun.Service{
		Name: constant.ServiceHTTP,
		Run: func() error {
			runErr := server.Run()
			// 停收可能先于监听，服务管理器将这种交错视为正常退出。
			if errors.Is(runErr, httpserver.ErrClosed) {
				return nil
			}
			return runErr
		},
		Stop:   server.Stop,
		Wait:   server.Wait,
		Cancel: server.Cancel,
	}, nil
}
