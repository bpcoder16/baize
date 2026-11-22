package outbox

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bpcoder16/baize/constant"
	model "github.com/bpcoder16/baize/model/outbox"
	"golang.org/x/net/http/httpguts"
)

// JetStreamMessage 使用原始 subject，由 natsx 添加 namespace；消息标识由登记生成的任务 ID 提供。
// MySQL 非零 ExpiresAt 的 UTC 整秒必须在 1 至 2147483647 范围内。
type JetStreamMessage struct {
	ResourceName string
	Subject      string
	Data         []byte
	Header       map[string][]string
	ExpiresAt    time.Time
}

func validateSubject(subject string) error {
	if subject == "" || !utf8.ValidString(subject) || strings.IndexFunc(subject, unicode.IsSpace) >= 0 || strings.ContainsAny(subject, "*>\x00") {
		return fmt.Errorf("%w: concrete NATS subject required", ErrInvalidArgument)
	}
	for token := range strings.SplitSeq(subject, ".") {
		if token == "" {
			return fmt.Errorf("%w: empty NATS subject token", ErrInvalidArgument)
		}
	}
	return nil
}

// task 在入队时固化目标和正文，重试时使用持久化的实例名。
func (msg JetStreamMessage) task(names []string) (model.Task, error) {
	if msg.ResourceName == "" {
		msg.ResourceName = names[0]
	} else if !slices.Contains(names, msg.ResourceName) {
		return model.Task{}, fmt.Errorf("%w: %q", ErrInvalidResource, msg.ResourceName)
	}
	if err := validateSubject(msg.Subject); err != nil {
		return model.Task{}, err
	}
	if msg.Data == nil || msg.Header == nil || msg.ExpiresAt.IsZero() {
		return model.Task{}, fmt.Errorf("%w: data, header and expires_at are required", ErrInvalidArgument)
	}
	// nats.go 的 Header 名使用 HTTP token 字符集，值由 SDK 在发布时规范化。
	for name := range msg.Header {
		if !httpguts.ValidHeaderFieldName(name) {
			return model.Task{}, fmt.Errorf("%w: invalid NATS header name %q", ErrInvalidArgument, name)
		}
	}
	payload, err := json.Marshal(model.JetStreamPayload{
		Subject: msg.Subject,
		Data:    msg.Data,
		Header:  msg.Header,
	})
	if err != nil {
		return model.Task{}, err
	}
	return model.Task{
		HandlerType:  constant.NatsJetStream,
		ResourceName: msg.ResourceName,
		Payload:      payload,
		ExpiresAt:    msg.ExpiresAt,
	}, nil
}
