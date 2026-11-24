// Package oss 定义 MySQL 上传记录的表结构。
package oss

import "time"

// Record 记录申请及确认状态，不保存临时凭据或下载签名。
type Record struct {
	ID                 uint64     `gorm:"column:id;type:bigint unsigned;primaryKey;autoIncrement;<-:create;comment:自增主键ID"`
	Scene              string     `gorm:"column:scene;type:varchar(64);not null;<-:create;comment:上传场景"`
	BucketType         string     `gorm:"column:bucket_type;type:varchar(16);not null;<-:create;comment:申请时的桶类型"`
	ObjectKey          string     `gorm:"column:object_key;type:varchar(500);not null;uniqueIndex:idx_baize_oss_upload_records_object_key;<-:create;comment:OSS对象Key"`
	WatermarkObjectKey string     `gorm:"column:watermark_object_key;type:varchar(500);not null;default:'';comment:水印图OSS对象Key"`
	FileExt            string     `gorm:"column:file_ext;type:varchar(20);not null;<-:create;comment:文件扩展名（含点、小写）"`
	FileSize           int64      `gorm:"column:file_size;type:bigint;not null;<-:create;comment:申请文件字节数"`
	ActualFileSize     *int64     `gorm:"column:actual_file_size;type:bigint;comment:实际文件字节数，NULL表示未采集"`
	Status             uint8      `gorm:"column:status;type:tinyint unsigned;not null;default:1;comment:状态1待确认2成功"`
	ConfirmedAt        *time.Time `gorm:"column:confirmed_at;type:timestamp(6) NULL;default:null;comment:确认时间"`
	CreatedAt          time.Time  `gorm:"column:created_at;type:timestamp(6);not null;default:CURRENT_TIMESTAMP(6);autoCreateTime:false;<-:create;comment:创建时间"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;type:timestamp(6);not null;default:CURRENT_TIMESTAMP(6);autoUpdateTime:false;comment:更新时间"`
}

func (Record) TableName() string {
	return "baize_oss_upload_records"
}
