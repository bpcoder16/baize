package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	outboxerr "github.com/bpcoder16/baize/error/outbox"
	model "github.com/bpcoder16/baize/model/outbox"
	"github.com/bpcoder16/pixiu/infra/natsx"
)

// JetStreamHandler 借用已验证的命名客户端。入队和投递共用同一份目标映射。
type JetStreamHandler struct {
	defaultName string
	clients     map[string]*natsx.Client
}

// NewJetStreamHandler 绑定默认名称和可投递的客户端。默认目标也必须在映射中。
func NewJetStreamHandler(defaultName string, clients map[string]*natsx.Client) (*JetStreamHandler, error) {
	for name, client := range clients {
		// resource_name 存为最多 64 字节的 ASCII 实例名。
		invalidChar := strings.ContainsFunc(name, func(r rune) bool { return r < '!' || r > '~' })
		if client == nil || name == "" || len(name) > 64 || invalidChar {
			return nil, fmt.Errorf("%w: invalid JetStream binding %q", outboxerr.ErrInvalidResource, name)
		}
	}
	if clients[defaultName] == nil {
		return nil, fmt.Errorf("%w: unknown default JetStream target %q", outboxerr.ErrInvalidResource, defaultName)
	}
	return &JetStreamHandler{defaultName: defaultName, clients: maps.Clone(clients)}, nil
}

// ResolveResource 仅在入队时补全默认目标；投递时使用任务里固化的名称。
func (h *JetStreamHandler) ResolveResource(name string) (string, error) {
	if name == "" {
		name = h.defaultName
	}
	if h.clients[name] == nil {
		return "", fmt.Errorf("%w: %q", outboxerr.ErrInvalidResource, name)
	}
	return name, nil
}

// Publish 使用任务 ID 去重，等待有效的 JetStream PubAck。
func (h *JetStreamHandler) Publish(ctx context.Context, delivery model.Delivery) error {
	// 先确认原目标，避免正文解码错误掩盖应停止重试的无效目标。
	client := h.clients[delivery.ResourceName]
	if client == nil {
		return fmt.Errorf("%w: %q", outboxerr.ErrInvalidResource, delivery.ResourceName)
	}
	var p model.JetStreamPayload
	if err := json.Unmarshal(delivery.Payload, &p); err != nil {
		return err
	}
	ack, err := client.PublishJetStream(ctx, p.Subject, p.Data, strconv.FormatInt(delivery.ID, 10), p.Header)
	if err != nil {
		return err
	}
	if ack == nil || ack.Stream == "" || ack.Sequence == 0 {
		return errors.New("outbox: missing or invalid JetStream PubAck")
	}
	return nil
}
