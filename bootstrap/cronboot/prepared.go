package cronboot

import (
	"context"
	"fmt"
	"slices"

	"github.com/bpcoder16/pixiu/infra/cronx"
)

type preparedJobs struct {
	handlers []cronx.NamedHandler
	jobs     []cronx.Job
}
type jobsKey struct{}

// AddJob 将模块的内置任务加入本次启动的 Cron，供串行 Prepare 步骤调用。
func AddJob(ctx context.Context, job cronx.Job) error {
	state, _ := ctx.Value(jobsKey{}).(*preparedJobs)
	if state == nil {
		return fmt.Errorf("cronboot: enabled cron service required for job %q", job.Name)
	}
	if slices.ContainsFunc(state.handlers, func(h cronx.NamedHandler) bool { return h.Name == job.Name }) ||
		slices.ContainsFunc(state.jobs, func(j cronx.Job) bool { return j.Name == job.Name }) {
		return fmt.Errorf("cronboot: duplicate job %q", job.Name)
	}
	state.jobs = append(state.jobs, job)
	return nil
}
