// Package oss 定义上传接口的请求与响应。
package oss

// OSSUploadTokenFileRequest 描述单个待上传文件。
type OSSUploadTokenFileRequest struct {
	// 客户端文件标识，同一次申请内不可重复，用于关联返回的上传记录。
	ClientFileID string `json:"clientFileId" binding:"required,min=1,max=64" example:"local-001"`
	// 文件扩展名，必填；大小写不敏感，可省略开头的点，服务端统一为含点的小写后缀。
	FileExt string `json:"fileExt" binding:"required,min=1,max=20" example:".json"`
	// 申请的文件大小，单位为字节，须符合场景配置的大小限制。
	FileSize int64 `json:"fileSize" binding:"required,min=1,max=1073741824" example:"1024"`
}

// OSSUploadTokenRequest 申请指定场景的 OSS 上传凭据。
type OSSUploadTokenRequest struct {
	// 上传场景，长度为 1 至 64 字节，须匹配配置的场景名称，不区分大小写。
	Scene string `json:"scene" binding:"required,min=1,max=64" example:"config-file"`
	// 待上传文件列表，数量须符合场景配置的批量限制。
	Files []OSSUploadTokenFileRequest `json:"files" binding:"required,min=1,max=100,dive"`
}

// OSSUploadConfirmItemRequest 确认单个已上传成功的文件。
type OSSUploadConfirmItemRequest struct {
	// 申请上传凭据时返回的上传记录 ID，同一次确认内不可重复。
	ID uint64 `json:"id" binding:"required,min=1" example:"1001"`
}

// OSSUploadConfirmRequest 批量确认待确认的上传记录。
type OSSUploadConfirmRequest struct {
	// 已上传成功的文件列表，每条记录仅允许确认一次。
	Items []OSSUploadConfirmItemRequest `json:"items" binding:"required,min=1,max=100,dive"`
}
