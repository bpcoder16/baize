// Package shareboot 将类型化分享页面装配到宿主的 Plan.Prepare。
package shareboot

import (
	"context"
	"errors"
	"path/filepath"
	"slices"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/service/share"
	"github.com/bpcoder16/pixiu/lifecycle"
)

// Prepare 读取 baize.share.yaml 并一次发布全部页面，路由由宿主自行挂载。
func Prepare(pages ...share.Registration) bootstrap.Step {
	pages = slices.Clone(pages)
	return bootstrap.Step{
		Name: "baize.share",
		Init: func(_ context.Context, dir string, stack *lifecycle.Stack) error {
			release, err := share.Load(filepath.Join(dir, "baize.share.yaml"), pages...)
			if err != nil {
				return err
			}
			if err = stack.Register(release); err != nil {
				return errors.Join(err, release())
			}
			return nil
		},
	}
}
