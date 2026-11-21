package natsboot

import (
	"errors"
	"fmt"
	"time"

	"github.com/bpcoder16/pixiu/infra/natsx"
	"github.com/nats-io/nats.go/jetstream"
)

// 订阅配置沿用 Biucards 的 nats-register.<实例名>.yaml 格式。
type registrationConfig struct {
	StreamDefaults struct {
		Replicas *int `mapstructure:"replicas"`
	} `mapstructure:"streamDefaults"`
	Streams       []streamConfig `mapstructure:"streams"`
	CoreConsumers []coreConfig   `mapstructure:"coreConsumers"`
}

type streamConfig struct {
	Name       string            `mapstructure:"name"`
	Mode       *natsx.StreamMode `mapstructure:"mode"`
	Subjects   []string          `mapstructure:"subjects"`
	Replicas   *int              `mapstructure:"replicas"`
	MaxBytes   *int64            `mapstructure:"maxBytes"`
	Retention  *string           `mapstructure:"retention"`
	MaxAge     *time.Duration    `mapstructure:"maxAge"`
	Duplicates *time.Duration    `mapstructure:"duplicates"`
	Consumers  []consumerConfig  `mapstructure:"consumers"`
}

type consumerConfig struct {
	Durable        string              `mapstructure:"durable"`
	Mode           *natsx.ConsumerMode `mapstructure:"mode"`
	FilterSubjects []string            `mapstructure:"filterSubjects"`
	Handler        string              `mapstructure:"handler"`
	AckWait        *time.Duration      `mapstructure:"ackWait"`
	BackOff        []time.Duration     `mapstructure:"backOff"`
	MaxDeliver     *int                `mapstructure:"maxDeliver"`
	MaxAckPending  *int                `mapstructure:"maxAckPending"`
	Consume        struct {
		Workers     *int `mapstructure:"workers"`
		MaxMessages *int `mapstructure:"maxMessages"`
	} `mapstructure:"consume"`
	RetryDelay *time.Duration `mapstructure:"retryDelay"`
}

type coreConfig struct {
	Name        string `mapstructure:"name"`
	Subject     string `mapstructure:"subject"`
	Queue       string `mapstructure:"queue"`
	Subscribers int    `mapstructure:"subscribers"`
	Handler     string `mapstructure:"handler"`
}

type registrations struct {
	streams            []natsx.StreamRegistration
	jetStreamConsumers []natsx.JetStreamConsumer
	coreConsumers      []natsx.CoreConsumer
}

func (cfg *registrationConfig) bind(urlCount int, jsRegistry *natsx.JetStreamHandlerRegistry, coreRegistry *natsx.CoreHandlerRegistry) (*registrations, error) {
	replicas := urlCount
	if cfg.StreamDefaults.Replicas != nil {
		replicas = *cfg.StreamDefaults.Replicas
	}
	if replicas <= 0 {
		return nil, errors.New("streamDefaults.replicas must be a positive integer")
	}
	jsHandlers := jsRegistry.Handlers()
	coreHandlers := coreRegistry.Handlers()
	batch := &registrations{}
	for i, spec := range cfg.Streams {
		stream, err := spec.streamRegistration(replicas)
		if err != nil {
			return nil, fmt.Errorf("streams[%d] %q %w", i, spec.Name, err)
		}
		batch.streams = append(batch.streams, stream)
		// 按父级及内部声明顺序展开，Stream 名称只有父级这一处配置来源。
		for j, consumerSpec := range spec.Consumers {
			consumer, bindErr := consumerSpec.consumer(spec.Name, jsHandlers)
			if bindErr != nil {
				return nil, fmt.Errorf("streams[%d].consumers[%d] %q/%q: %w", i, j, spec.Name, consumerSpec.Durable, bindErr)
			}
			batch.jetStreamConsumers = append(batch.jetStreamConsumers, consumer)
		}
	}
	for i, spec := range cfg.CoreConsumers {
		if spec.Queue != "" && spec.Subscribers <= 1 {
			return nil, fmt.Errorf("coreConsumers[%d] %q subscribers %d must be greater than 1 when queue is set", i, spec.Name, spec.Subscribers)
		}
		handler, ok := coreHandlers[spec.Handler]
		if !ok {
			return nil, fmt.Errorf("coreConsumers[%d] %q: unknown Core handler %q", i, spec.Name, spec.Handler)
		}
		// queue 为空时沿用广播语义，订阅数由 natsx 固定为 1。
		batch.coreConsumers = append(
			batch.coreConsumers,
			natsx.DefaultCoreQueueConsumer(
				spec.Name,
				spec.Subject,
				spec.Queue,
				spec.Subscribers,
				handler,
			),
		)
	}
	return batch, nil
}

func (spec consumerConfig) consumer(stream string, handlers map[string]natsx.JetStreamHandler) (natsx.JetStreamConsumer, error) {
	handler, ok := handlers[spec.Handler]
	if !ok {
		return natsx.JetStreamConsumer{}, fmt.Errorf("unknown JetStream handler %q", spec.Handler)
	}
	consumer := natsx.DefaultJetStreamConsumer(
		stream,
		spec.Durable,
		handler,
		spec.FilterSubjects...,
	)
	if spec.Mode != nil {
		switch *spec.Mode {
		case natsx.ConsumerModeCreate, natsx.ConsumerModeUpsert:
			consumer.Mode = *spec.Mode
		default:
			return natsx.JetStreamConsumer{}, fmt.Errorf("mode %q must be one of create, upsert", *spec.Mode)
		}
	}
	// 仅覆盖显式配置的字段，保留零值语义，由 natsx 和服务端统一归一化及校验。
	if spec.AckWait != nil {
		consumer.Config.AckWait = *spec.AckWait
	}
	if len(spec.BackOff) > 0 {
		consumer.Config.BackOff = spec.BackOff
	}
	if spec.MaxDeliver != nil {
		consumer.Config.MaxDeliver = *spec.MaxDeliver
	}
	if spec.MaxAckPending != nil {
		consumer.Config.MaxAckPending = *spec.MaxAckPending
	}
	if spec.Consume.Workers != nil {
		consumer.Consume.Workers = *spec.Consume.Workers
	}
	if spec.Consume.MaxMessages != nil {
		consumer.Consume.MaxMessages = *spec.Consume.MaxMessages
	}
	if spec.RetryDelay != nil {
		consumer.RetryDelay = *spec.RetryDelay
	}
	return consumer, nil
}

// streamRegistration 按模式组装声明；existing 保留名称，不填充或比较 Stream 默认配置。
func (spec streamConfig) streamRegistration(replicas int) (natsx.StreamRegistration, error) {
	mode := natsx.StreamModeCreate
	if spec.Mode != nil {
		mode = *spec.Mode
	}
	registration := natsx.StreamRegistration{
		Mode:   mode,
		Config: jetstream.StreamConfig{Name: spec.Name},
	}
	switch mode {
	case natsx.StreamModeExisting:
		if len(spec.Subjects) != 0 || spec.Replicas != nil || spec.MaxBytes != nil ||
			spec.Retention != nil || spec.MaxAge != nil || spec.Duplicates != nil {
			return registration, errors.New("existing mode does not accept subjects, replicas, maxBytes, retention, maxAge or duplicates")
		}
		return registration, nil
	case natsx.StreamModeCreate, natsx.StreamModeUpsert:
	default:
		return registration, fmt.Errorf("mode %q must be one of create, upsert, existing", mode)
	}
	if len(spec.Subjects) == 0 {
		return registration, errors.New("requires nonempty subjects")
	}
	effectiveReplicas := replicas
	if spec.Replicas != nil && *spec.Replicas > 0 {
		if *spec.Replicas > replicas {
			return registration, fmt.Errorf("replicas %d must not exceed streamDefaults.replicas %d", *spec.Replicas, replicas)
		}
		effectiveReplicas = *spec.Replicas
	}
	stream := natsx.DefaultStreamConfig(spec.Name, effectiveReplicas, spec.Subjects...)
	// 仅覆盖显式配置，保留零值语义；范围及组合约束交给服务端。
	if spec.MaxBytes != nil {
		stream.MaxBytes = *spec.MaxBytes
	}
	if spec.Retention != nil {
		switch *spec.Retention {
		case "limits":
			stream.Retention = jetstream.LimitsPolicy
		case "interest":
			stream.Retention = jetstream.InterestPolicy
		case "workqueue":
			stream.Retention = jetstream.WorkQueuePolicy
		default:
			return registration, fmt.Errorf("retention %q must be one of limits, interest, workqueue", *spec.Retention)
		}
	}
	if spec.MaxAge != nil {
		stream.MaxAge = *spec.MaxAge
	}
	if spec.Duplicates != nil {
		stream.Duplicates = *spec.Duplicates
	}
	registration.Config = stream
	return registration, nil
}
