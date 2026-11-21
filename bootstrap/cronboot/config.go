package cronboot

import (
	"fmt"
	"strings"
	"time"

	"github.com/bpcoder16/pixiu/infra/cronx"
	"github.com/bpcoder16/pixiu/logit"
)

type fileConfig struct {
	LoggerName string      `mapstructure:"loggerName"`
	Lock       *lockConfig `mapstructure:"lock"`
	Jobs       []jobConfig `mapstructure:"jobs"`
}

type jobConfig struct {
	Name string `mapstructure:"name"`
	// 指针保留调度字段是否出现，混用两种调度时即使其中一个为零值也报错。
	Cron         *string               `mapstructure:"cron"`
	Every        *time.Duration        `mapstructure:"every"`
	RandomEvery  *cronx.RandomInterval `mapstructure:"randomEvery"`
	Timeout      time.Duration         `mapstructure:"timeout"`
	SuccessLevel string                `mapstructure:"successLevel"`
}

func (cfg *fileConfig) bind(handlers []cronx.NamedHandler) ([]cronx.Job, error) {
	byName := make(map[string]cronx.Handler, len(handlers))
	for _, h := range handlers {
		if h.Name == "" || strings.TrimSpace(h.Name) != h.Name || h.Handler == nil {
			return nil, fmt.Errorf("invalid handler %q", h.Name)
		}
		if _, exists := byName[h.Name]; exists {
			return nil, fmt.Errorf("duplicate handler %q", h.Name)
		}
		byName[h.Name] = h.Handler
	}
	jobs := make([]cronx.Job, 0, len(cfg.Jobs))
	for _, spec := range cfg.Jobs {
		handler, ok := byName[spec.Name]
		if !ok {
			return nil, fmt.Errorf("unknown handler %q", spec.Name)
		}
		job, err := spec.job(handler)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (spec jobConfig) job(handler cronx.Handler) (cronx.Job, error) {
	job := cronx.Job{Name: spec.Name, Handler: handler, Timeout: spec.Timeout}
	count := 0
	if spec.Cron != nil {
		job.Cron = *spec.Cron
		count++
	}
	if spec.Every != nil {
		job.Every = *spec.Every
		count++
	}
	if spec.RandomEvery != nil {
		job.RandomEvery = spec.RandomEvery
		count++
	}
	if count != 1 {
		return cronx.Job{}, fmt.Errorf("job %q requires exactly one of cron, every, randomEvery", spec.Name)
	}
	switch spec.SuccessLevel {
	case "", "info":
		job.SuccessLevel = logit.InfoLevel
	case "debug":
		job.SuccessLevel = logit.DebugLevel
	default:
		return cronx.Job{}, fmt.Errorf("job %q: invalid successLevel %q", spec.Name, spec.SuccessLevel)
	}
	return job, nil
}
