package bootstrap

import (
	"context"
	"strings"
	"testing"
)

func TestLookupDeclared(t *testing.T) {
	ctx := context.WithValue(t.Context(), instancesKey{}, []Resource{
		{Name: "mysql", instances: []string{"audit", "orders"}},
		{Name: "postgresql", instances: []string{"archive"}},
	})
	t.Run("仅在其他资源类型声明", func(t *testing.T) {
		client, err := LookupDeclared(ctx, "mysql", "archive", func(string) *int {
			t.Fatal("未声明时不应查找已注册实例")
			return nil
		})
		if client != nil || err == nil || !strings.Contains(err.Error(), `mysql instance "archive" is not declared`) {
			t.Fatalf("应返回未声明错误，得到实例 %v，错误 %v", client, err)
		}
	})
	t.Run("已声明且已注册", func(t *testing.T) {
		client, err := LookupDeclared(ctx, "mysql", "orders", func(name string) string {
			return name
		})
		if client != "orders" || err != nil {
			t.Fatalf("应返回 orders 实例，得到 %q，错误 %v", client, err)
		}
	})
	t.Run("已声明但未注册", func(t *testing.T) {
		client, err := LookupDeclared(ctx, "mysql", "orders", func(string) *int {
			panic("client is not registered")
		})
		if client != nil || err == nil || !strings.Contains(err.Error(), "client is not registered") {
			t.Fatalf("应返回注册表查找错误，得到实例 %v，错误 %v", client, err)
		}
	})
}
