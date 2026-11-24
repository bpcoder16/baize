package oss

import "time"

// Record 是上传业务和仓储共用的记录。
type Record struct {
	ID                 uint64
	Scene              string
	BucketType         string
	ObjectKey          string
	WatermarkObjectKey string
	FileExt            string
	FileSize           int64
	ActualFileSize     *int64
	Status             uint8
	ConfirmedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

const (
	Pending uint8 = 1 // 申请成功，等待确认。
	Success uint8 = 2 // 已完成上传。
)
