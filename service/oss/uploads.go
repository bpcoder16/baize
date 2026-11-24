package oss

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/bpcoder16/baize/display"
	model "github.com/bpcoder16/baize/model/oss"
	repo "github.com/bpcoder16/baize/repository/oss"
	"github.com/bpcoder16/pixiu/infra/aliyun/ossx"
	"github.com/bpcoder16/pixiu/infra/aliyun/stsx"
	"github.com/bpcoder16/pixiu/infra/env"
)

var (
	ErrParams   = errors.New("invalid OSS upload parameters")
	ErrScene    = errors.New("invalid OSS upload scene")
	ErrFileType = errors.New("invalid OSS upload file type")
	ErrNotFound = errors.New("OSS upload record not found")
	ErrState    = errors.New("OSS upload state mismatch")
)

// Uploads 管理上传记录和命名桶。服务就绪后可供三端并发使用。
type Uploads struct {
	config  Config
	store   *repo.Store
	buckets bucketServices
}

// BucketClients 指定公有桶和私有桶的客户端，未配置的桶字段留空。
type BucketClients struct {
	Public  *ossx.Client
	Private *ossx.Client
}

// UploadBuckets 保存已匹配的场景配置和桶客户端，可在打开仓储前准备。
type UploadBuckets struct {
	config   *Config
	services bucketServices
}

type bucketServices struct {
	public  *Service
	private *Service
}

func (b bucketServices) lookup(bucketType string) (*Service, error) {
	var service *Service
	switch bucketType {
	case "public":
		service = b.public
	case "private":
		service = b.private
	}
	if service == nil {
		return nil, fmt.Errorf("oss: missing bucket binding %q", bucketType)
	}
	return service, nil
}

// BindBuckets 按 public、private 匹配场景和客户端。cfg 由 LoadConfig 加载，绑定后只读。
func BindBuckets(cfg *Config, clients BucketClients, sts *stsx.Client) (*UploadBuckets, error) {
	if cfg == nil || cfg.PrivateBucketURLExpires == nil || sts == nil {
		return nil, errors.New("oss: loaded upload config and STS client required")
	}
	var buckets bucketServices
	var err error
	if clients.Public != nil {
		buckets.public, err = New(clients.Public, WithSTSClient(sts))
		if err != nil {
			return nil, err
		}
	}
	if clients.Private != nil {
		buckets.private, err = New(clients.Private, WithSTSClient(sts))
		if err != nil {
			return nil, err
		}
	}
	for name, scene := range cfg.Scenes {
		if _, lookupErr := buckets.lookup(scene.BucketType); lookupErr != nil {
			return nil, fmt.Errorf("oss: scene %q requires bucket %q", name, scene.BucketType)
		}
	}
	return &UploadBuckets{config: cfg, services: buckets}, nil
}

// NewUploads 使用就绪的仓储和已绑定的桶创建上传服务。
func NewUploads(store *repo.Store, buckets *UploadBuckets) (*Uploads, error) {
	if store == nil || buckets == nil || buckets.config == nil {
		return nil, errors.New("oss: upload store and bound buckets required")
	}
	return &Uploads{
		config:  *buckets.config,
		store:   store,
		buckets: buckets.services,
	}, nil
}

var active atomic.Pointer[Uploads]

// Default 返回 Prepare 已装配的上传服务，启动前和退出后返回 nil。
func Default() *Uploads { return active.Load() }

// Register 在 Prepare 完成迁表后发布服务，返回本次启动的回收函数。
func Register(s *Uploads) (func() error, error) {
	if s == nil || s.store == nil || !active.CompareAndSwap(nil, s) {
		return nil, errors.New("oss: cannot register upload service")
	}
	release := sync.OnceFunc(func() { active.CompareAndSwap(s, nil) })
	return func() error {
		release()
		return nil
	}, nil
}

// AllCompleted 判断指定 ID 是否全部存在、上传成功且属于指定场景。按上传场景的相同小写规则匹配历史记录。
// 空白场景、空列表或含零 ID 时返回 false, nil。其余请求按 ID 去重后读从库，查询错误原样返回。
func (s *Uploads) AllCompleted(ctx context.Context, scene string, ids []uint64) (bool, error) {
	if strings.TrimSpace(scene) == "" || len(ids) == 0 {
		return false, nil
	}
	seen := make(map[uint64]bool, len(ids))
	uniqueIDs := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			return false, nil
		}
		if !seen[id] {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	records, err := s.store.Find(ctx, uniqueIDs)
	if err != nil {
		return false, err
	}
	if len(records) != len(uniqueIDs) {
		return false, nil
	}
	sceneKey := strings.ToLower(scene)
	for _, record := range records {
		if record.Status != model.Success || strings.ToLower(record.Scene) != sceneKey {
			return false, nil
		}
	}
	return true, nil
}

// DisplayURLMap 按 ID 查询从库中的上传记录并返回展示地址，优先使用水印对象，水印 Key 为空时使用原始对象。
// 空输入返回空 map，缺失的 ID 跳过，待确认记录也生成地址。查询或生成地址失败时返回 nil 和错误。
// 宿主负责访问授权，需要校验上传状态和场景时可先调用 AllCompleted。
func (s *Uploads) DisplayURLMap(ctx context.Context, ids []uint64) (map[uint64]string, error) {
	return s.urlMap(ctx, ids, true, s.store.Find)
}

// OriginURLMap 按 ID 查询从库中的上传记录并返回原始对象地址。批量查询和错误处理规则同 [Uploads.DisplayURLMap]。
func (s *Uploads) OriginURLMap(ctx context.Context, ids []uint64) (map[uint64]string, error) {
	return s.urlMap(ctx, ids, false, s.store.Find)
}

// DisplayURLMapOnMaster 从主库读取上传记录并返回展示地址，地址生成规则同 [Uploads.DisplayURLMap]。
func (s *Uploads) DisplayURLMapOnMaster(ctx context.Context, ids []uint64) (map[uint64]string, error) {
	return s.urlMap(ctx, ids, true, s.store.FindOnMaster)
}

// OriginURLMapOnMaster 从主库读取上传记录并返回原始对象地址，地址生成规则同 [Uploads.OriginURLMap]。
func (s *Uploads) OriginURLMapOnMaster(ctx context.Context, ids []uint64) (map[uint64]string, error) {
	return s.urlMap(ctx, ids, false, s.store.FindOnMaster)
}

func (s *Uploads) urlMap(ctx context.Context, ids []uint64, preferWatermark bool, find func(context.Context, []uint64) ([]model.Record, error)) (map[uint64]string, error) {
	records, err := find(ctx, ids)
	if err != nil {
		return nil, err
	}
	urls := make(map[uint64]string, len(records))
	for _, record := range records {
		key := record.ObjectKey
		if preferWatermark && record.WatermarkObjectKey != "" {
			key = record.WatermarkObjectKey
		}
		objectURL, urlErr := s.objectURL(ctx, record.BucketType, key)
		if urlErr != nil {
			return nil, urlErr
		}
		urls[record.ID] = objectURL
	}
	return urls, nil
}

func (s *Uploads) objectURL(ctx context.Context, bucketType, key string) (string, error) {
	if bucketType == "public" {
		if s.config.PublicBucketCDN == "" {
			return "", errors.New("oss: publicBucketCDN is required for public downloads")
		}
		object := url.URL{Path: "/" + key}
		return strings.TrimRight(s.config.PublicBucketCDN, "/") + object.EscapedPath(), nil
	}
	bucket, err := s.buckets.lookup(bucketType)
	if err != nil {
		return "", err
	}
	return bucket.oss.PresignGetObject(ctx, key, *s.config.PrivateBucketURLExpires)
}

// MaxDirectUploadSize 返回表单解析时使用的全局文件上限。
func (s *Uploads) MaxDirectUploadSize() int64 { return s.config.MaxDirectUploadSize() }

func (s *Uploads) lookupScene(name string) (Scene, bool) {
	// 大小写转换可能改变字节数，先检查调用方传入的原始名称。
	if !validSceneName(name) {
		return Scene{}, false
	}
	scene, ok := s.config.Scenes[strings.ToLower(name)]
	return scene, ok
}

// CreateToken 按场景签发上传凭据并登记记录。
func (s *Uploads) CreateToken(ctx context.Context, req model.OSSUploadTokenRequest) (model.OSSUploadTokenResponse, error) {
	var result model.OSSUploadTokenResponse
	scene, ok := s.lookupScene(req.Scene)
	if !ok {
		return result, ErrScene
	}
	if len(req.Files) == 0 || len(req.Files) > scene.MaxBatchCount {
		return result, ErrSize
	}
	seen := make(map[string]bool, len(req.Files))
	records := make([]model.Record, 0, len(req.Files))
	keys := make([]string, 0, len(req.Files))
	for _, file := range req.Files {
		if file.ClientFileID == "" || utf8.RuneCountInString(file.ClientFileID) > 64 || seen[file.ClientFileID] {
			return result, ErrParams
		}
		seen[file.ClientFileID] = true
		ext, validateErr := scene.validateFile(file.FileExt, file.FileSize)
		if validateErr != nil {
			return result, validateErr
		}
		key := scene.ObjectKey(ext)
		keys = append(keys, key)
		records = append(records, model.Record{
			Scene:      req.Scene,
			BucketType: scene.BucketType,
			ObjectKey:  key,
			FileExt:    ext,
			FileSize:   file.FileSize,
			Status:     model.Pending,
		})
	}
	uploadService, err := s.buckets.lookup(scene.BucketType)
	if err != nil {
		return result, err
	}
	upload, err := uploadService.CreateUploadCredentials(ctx, keys, UploadCredentialsOptions{
		RoleARN:     s.config.STSRoleARN,
		SessionName: s.config.STSSessionName,
		Duration:    scene.STSDuration,
	})
	if err != nil {
		return result, err
	}
	// STS 成功后才创建记录；数据库失败时不把凭据交给前端。
	if err = s.store.Create(ctx, records); err != nil {
		return result, err
	}
	info := upload.Info
	credential := upload.Credentials
	result = model.OSSUploadTokenResponse{
		Endpoint:        info.Endpoint,
		Region:          info.Region,
		BucketName:      info.Bucket,
		AccessKeyID:     credential.AccessKeyID,
		AccessKeySecret: credential.AccessKeySecret,
		SecurityToken:   credential.SecurityToken,
		Expiration:      display.FormatTime(&credential.Expiration),
		Items:           make([]model.OSSUploadTokenItem, len(records)),
	}
	for i, record := range records {
		result.Items[i] = model.OSSUploadTokenItem{
			ClientFileID: req.Files[i].ClientFileID,
			ID:           record.ID,
			ObjectKey:    record.ObjectKey,
		}
	}
	return result, nil
}

// Confirm 从主库读取上传记录，核对 OSS 对象及实际大小，仅允许待确认记录在同一事务内变为成功。
func (s *Uploads) Confirm(ctx context.Context, req model.OSSUploadConfirmRequest) error {
	if len(req.Items) == 0 || len(req.Items) > 100 {
		return ErrParams
	}
	ids := make([]uint64, 0, len(req.Items))
	seen := make(map[uint64]bool, len(req.Items))
	for _, item := range req.Items {
		if item.ID == 0 || seen[item.ID] {
			return ErrParams
		}
		seen[item.ID] = true
		ids = append(ids, item.ID)
	}
	records, err := s.store.FindOnMaster(ctx, ids)
	if err != nil {
		return err
	}
	byID := make(map[uint64]model.Record, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	updates := make([]model.Record, 0, len(req.Items))
	for _, item := range req.Items {
		record, ok := byID[item.ID]
		if !ok {
			return ErrNotFound
		}
		if record.Status != model.Pending {
			return ErrState
		}
		// 使用记录中的桶类型和对象 Key，确保校验申请时授权的对象。
		client, lookupErr := s.buckets.lookup(record.BucketType)
		if lookupErr != nil {
			return lookupErr
		}
		info, headErr := client.oss.HeadObject(ctx, record.ObjectKey)
		if headErr != nil {
			return headErr
		}
		// 保留申请大小，确认时单独记录 OSS 返回的实际大小。
		record.ActualFileSize = &info.ContentLength
		updates = append(updates, record)
	}
	err = s.store.Confirm(ctx, updates)
	if errors.Is(err, repo.ErrStateChanged) {
		return ErrState
	}
	return err
}

// DirectUpload 按场景上传文件并登记记录。
func (s *Uploads) DirectUpload(ctx context.Context, sceneName string, file *multipart.FileHeader) (model.OSSDirectUploadResponse, error) {
	var result model.OSSDirectUploadResponse
	scene, ok := s.lookupScene(sceneName)
	if !ok {
		return result, ErrScene
	}
	if file == nil {
		return result, ErrParams
	}
	ext, err := scene.validateFile(path.Ext(file.Filename), file.Size)
	if err != nil {
		return result, err
	}
	uploadService, err := s.buckets.lookup(scene.BucketType)
	if err != nil {
		return result, err
	}
	key := scene.ObjectKey(ext)
	if _, err = uploadService.UploadMultipart(ctx, key, file); err != nil {
		return result, err
	}
	records := []model.Record{{
		Scene:          sceneName,
		BucketType:     scene.BucketType,
		ObjectKey:      key,
		FileExt:        ext,
		FileSize:       file.Size,
		ActualFileSize: &file.Size,
		Status:         model.Success,
	}}
	if err = s.store.Create(ctx, records); err != nil {
		return result, err
	}
	return model.OSSDirectUploadResponse{
		ID:        records[0].ID,
		ObjectKey: key,
	}, nil
}

func (scene Scene) validateFile(ext string, size int64) (string, error) {
	if size < 1 || size > scene.MaxSize {
		return "", ErrSize
	}
	ext = strings.ToLower(ext)
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if !slices.Contains(scene.AllowedExts, ext) {
		return "", ErrFileType
	}
	return ext, nil
}

// ObjectKey 按运行模式、场景目录、应用日期和 UUID 生成新的对象名。
func (scene Scene) ObjectKey(ext string) string {
	return env.RunMode() + "/" + scene.TargetDir + "/" + time.Now().In(env.TimeLocation()).Format("20060102") + "/" + strings.ReplaceAll(uuid.New().String(), "-", "") + ext
}
