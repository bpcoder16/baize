// Package db 按后端借用本次 Plan 声明的数据库实例。
package db

import (
	"context"
	"fmt"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/mysqlx"
	"github.com/bpcoder16/pixiu/infra/postgresqlx"
	"gorm.io/gorm"
)

// Client 提供同一个命名实例的主、从库会话。
type Client interface {
	MasterDB(context.Context) *gorm.DB
	SlaveDB(context.Context) *gorm.DB
}

// LookupClient 借用本次 Plan 声明的 MySQL 或 PostgreSQL 实例。
func LookupClient(ctx context.Context, backend constant.Backend, name string) (Client, error) {
	var client Client
	var err error
	switch backend {
	case constant.MySQL:
		client, err = bootstrap.LookupDeclared(ctx, constant.ResourceMySQL, name, mysqlx.Named)
	case constant.PostgreSQL:
		client, err = bootstrap.LookupDeclared(ctx, constant.ResourcePostgreSQL, name, postgresqlx.Named)
	default:
		return nil, fmt.Errorf("unsupported database backend %q", backend)
	}
	if err != nil {
		return nil, err
	}
	return client, nil
}
