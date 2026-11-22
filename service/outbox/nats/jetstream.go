package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"

	outboxerr "github.com/bpcoder16/baize/error/outbox"
	model "github.com/bpcoder16/baize/model/outbox"
	"github.com/bpcoder16/pixiu/infra/natsx"
)

// JetStreamHandler 借用由 Prepare 验证过的命名客户端。
type JetStreamHandler struct {
	clients map[string]*natsx.Client
}

func NewJetStreamHandler(clients map[string]*natsx.Client) *JetStreamHandler {
	return &JetStreamHandler{clients: maps.Clone(clients)}
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
