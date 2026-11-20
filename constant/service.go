// Package constant 定义白泽与宿主共用的常量。
package constant

// 常驻服务类型用于服务声明和 disabledServices 规则。
const (
	ServiceHTTP         = "http"
	ServiceCron         = "cron"
	ServiceNATSConsumer = "nats-consumer"
	ServiceWebSocket    = "websocket"
)

// IsServiceType 判断 kind 是否为 disabledServices 支持的服务类型。
func IsServiceType(kind string) bool {
	switch kind {
	case ServiceHTTP, ServiceCron, ServiceNATSConsumer, ServiceWebSocket:
		return true
	default:
		return false
	}
}
