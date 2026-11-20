package bootstrap

import (
	"context"
	"fmt"

	"github.com/bpcoder16/pixiu/lifecycle"
)

// Step 在共享资源就绪后执行。Init 可使用 ctx 中止工作，通过栈登记本步骤新取得的资源。
type Step struct {
	Name string
	Init func(ctx context.Context, configDir string, resources *lifecycle.Stack) error
}

func validatePrepare(steps []Step) error {
	names := make(map[string]bool, len(steps))
	for _, step := range steps {
		if err := validateEntry(names, "prepare", step.Name, step.Init != nil); err != nil {
			return err
		}
	}
	return nil
}

func runPrepare(ctx context.Context, dir string, steps []Step, resources *lifecycle.Stack) error {
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := step.Init(ctx, dir, resources); err != nil {
			return fmt.Errorf("bootstrap: prepare %q: %w", step.Name, err)
		}
	}
	return nil
}
