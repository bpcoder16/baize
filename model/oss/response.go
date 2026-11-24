package oss

// OSSUploadTokenItem 返回单个文件的上传记录和授权对象。
type OSSUploadTokenItem struct {
	// 客户端文件标识，与申请中的 clientFileId 一致。
	ClientFileID string `json:"clientFileId" validate:"required" example:"local-001"`
	// 上传记录 ID，确认上传结果时回传。
	ID uint64 `json:"id" validate:"required" example:"1001"`
	// 本次凭据允许上传的 OSS 对象 Key，包含运行模式前缀，上传时须原样使用。
	ObjectKey string `json:"objectKey" validate:"required" example:"test/config-file/20261027/550e8400e29b41d4a716446655440000.json"`
}

// OSSUploadTokenResponse 返回临时上传凭据及其授权对象列表。
type OSSUploadTokenResponse struct {
	// OSS 上传服务地址。
	Endpoint string `json:"endpoint" validate:"required" example:"https://oss-cn-hangzhou.aliyuncs.com"`
	// OSS Bucket 所在地域。
	Region string `json:"region" validate:"required" example:"cn-hangzhou"`
	// 本次上传使用的 OSS Bucket 名称。
	BucketName string `json:"bucketName" validate:"required" example:"example-bucket"`
	// STS 临时访问密钥 ID。
	AccessKeyID string `json:"accessKeyId" validate:"required" example:"example-access-key-id"`
	// STS 临时访问密钥 Secret。
	AccessKeySecret string `json:"accessKeySecret" validate:"required" example:"example-access-key-secret"`
	// STS 安全令牌，须与临时访问密钥配套使用。
	SecurityToken string `json:"securityToken" validate:"required" example:"example-security-token"`
	// 临时凭据过期时间，使用应用配置时区的 RFC3339 格式，项目默认东八区。
	Expiration string `json:"expiration" validate:"required" format:"date-time" example:"2026-10-27T16:40:00+08:00"`
	// 文件上传记录列表，通过 clientFileId 与申请中的文件关联。
	Items []OSSUploadTokenItem `json:"items" validate:"required"`
}

// OSSDirectUploadResponse 返回服务端直传成功的文件信息。
type OSSDirectUploadResponse struct {
	// 已上传成功的记录 ID，无需再次确认。
	ID uint64 `json:"id" validate:"required" example:"1001"`
	// OSS 对象 Key，包含运行模式前缀，访问域名由调用方配置。
	ObjectKey string `json:"objectKey" validate:"required" example:"test/config-file/20261027/550e8400e29b41d4a716446655440000.json"`
}
