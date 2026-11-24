package oss

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/internal/testdb"
	model "github.com/bpcoder16/baize/model/oss"
	repo "github.com/bpcoder16/baize/repository/oss"
	"github.com/bpcoder16/pixiu/infra/aliyun/ossx"
	"github.com/bpcoder16/pixiu/infra/aliyun/stsx"
	"github.com/bpcoder16/pixiu/infra/env"
	"gorm.io/gorm"
)

func TestBindBuckets(t *testing.T) {
	publicClient := new(ossx.Client)
	privateClient := new(ossx.Client)
	sts := new(stsx.Client)
	expires := time.Minute
	for _, tc := range []struct {
		name        string
		clients     BucketClients
		bucketTypes []string
		wantErr     string
	}{
		{
			name:        "public only",
			clients:     BucketClients{Public: publicClient},
			bucketTypes: []string{"public"},
		},
		{
			name:        "private only",
			clients:     BucketClients{Private: privateClient},
			bucketTypes: []string{"private"},
		},
		{
			name: "public and private",
			clients: BucketClients{
				Public:  publicClient,
				Private: privateClient,
			},
			bucketTypes: []string{"public", "private"},
		},
		{
			name:        "missing private",
			clients:     BucketClients{Public: publicClient},
			bucketTypes: []string{"private"},
			wantErr:     `oss: scene "private" requires bucket "private"`,
		},
		{
			name:        "missing public",
			clients:     BucketClients{Private: privateClient},
			bucketTypes: []string{"public"},
			wantErr:     `oss: scene "public" requires bucket "public"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenes := make(map[string]Scene, len(tc.bucketTypes))
			for _, kind := range tc.bucketTypes {
				scenes[kind] = Scene{BucketType: kind}
			}
			cfg := &Config{
				PrivateBucketURLExpires: &expires,
				Scenes:                  scenes,
			}
			bound, err := BindBuckets(cfg, tc.clients, sts)
			if tc.wantErr != "" {
				if bound != nil || err == nil || err.Error() != tc.wantErr {
					t.Fatalf("应返回 %q，得到绑定 %v，错误 %v", tc.wantErr, bound, err)
				}
				return
			}
			if err != nil || bound == nil {
				t.Fatalf("应成功绑定客户端，得到 %v", err)
			}
			for _, want := range []struct {
				kind   string
				client *ossx.Client
			}{
				{"public", tc.clients.Public},
				{"private", tc.clients.Private},
				{"unknown", nil},
			} {
				service, lookupErr := bound.services.lookup(want.kind)
				if want.client == nil {
					wantErr := fmt.Sprintf("oss: missing bucket binding %q", want.kind)
					if service != nil || lookupErr == nil || lookupErr.Error() != wantErr {
						t.Fatalf("应返回 %q，得到服务 %v，错误 %v", wantErr, service, lookupErr)
					}
					continue
				}
				if lookupErr != nil || service == nil || service.oss != want.client || service.sts != sts {
					t.Fatalf("%q 桶的客户端绑定不正确，得到 %v", want.kind, lookupErr)
				}
			}
		})
	}
}

func TestValidateFile(t *testing.T) {
	scene := Scene{MaxSize: 1024, AllowedExts: []string{".json"}}
	for _, tc := range []struct {
		name string
		ext  string
		size int64
		want string
		err  error
	}{
		{"allowed extension", ".json", 1, ".json", nil},
		{"normalize extension", "JSON", 1, ".json", nil},
		{"normalize case", ".JsOn", 1, ".json", nil},
		{"maximum size", ".json", 1024, ".json", nil},
		{"empty extension", "", 1, "", ErrFileType},
		{"unsupported extension", ".exe", 1, "", ErrFileType},
		{"trailing whitespace", ".json ", 1, "", ErrFileType},
		{"path in extension", ".json/file", 1, "", ErrFileType},
		{"empty file", ".json", 0, "", ErrSize},
		{"oversize file", ".json", 1025, "", ErrSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext, err := scene.validateFile(tc.ext, tc.size)
			if ext != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("validateFile(%q, %d) = %q, %v; want %q, %v", tc.ext, tc.size, ext, err, tc.want, tc.err)
			}
		})
	}
}

func TestObjectKeyUsesUUID(t *testing.T) {
	// env 只能初始化一次，用子进程隔离运行模式和应用时区；主机时区固定为 UTC。
	mode := os.Getenv("BAIZE_OSS_OBJECT_KEY_TEST_MODE")
	if mode == "" {
		for _, mode := range []string{env.RunModeDebug, env.RunModeTest, env.RunModeRelease} {
			for _, zone := range []string{"Asia/Shanghai", "UTC"} {
				cmd := exec.Command(os.Args[0], "-test.run=^TestObjectKeyUsesUUID$")
				cmd.Env = append(os.Environ(), "BAIZE_OSS_OBJECT_KEY_TEST_MODE="+mode, "BAIZE_OSS_OBJECT_KEY_TEST_TIMEZONE="+zone, "TZ=UTC")
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s/%s: %v\n%s", mode, zone, err, output)
				}
			}
		}
		return
	}
	zone := os.Getenv("BAIZE_OSS_OBJECT_KEY_TEST_TIMEZONE")
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	if err := env.Init(env.Config{AppName: "oss-key-test", RunMode: mode, TimeLocation: zone, ConfigDirPath: t.TempDir(), LocalIP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	key := (Scene{TargetDir: "config-file"}).ObjectKey(".json")
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != mode || parts[1] != "config-file" || !strings.HasSuffix(parts[3], ".json") {
		t.Fatalf("unexpected object key structure: %q", key)
	}
	if _, err := time.Parse("20060102", parts[2]); err != nil {
		t.Fatalf("invalid object key date: %q", key)
	}
	name := strings.TrimSuffix(parts[3], ".json")
	if len(name) != 32 || strings.Contains(name, "-") {
		t.Fatalf("object key must use a 32-character UUID without hyphens: %q", key)
	}
	id, err := uuid.Parse(name)
	if err != nil || id == uuid.Nil() || strings.ReplaceAll(id.String(), "-", "") != name {
		t.Fatalf("object key must use a lowercase hexadecimal UUID: %q, %v", key, err)
	}
	synctest.Test(t, func(t *testing.T) {
		// 虚拟时钟从 2000-01-01 00:00 UTC 开始，推进至上海已跨日、UTC 尚未跨日的时刻。
		time.Sleep(20 * time.Hour)
		boundaryKey := (Scene{TargetDir: "config-file"}).ObjectKey(".json")
		wantDate := map[string]string{"Asia/Shanghai": "20000102", "UTC": "20000101"}[zone]
		if date := strings.Split(boundaryKey, "/")[2]; date != wantDate {
			t.Fatalf("object key date = %q, want %q for %s: %q", date, wantDate, zone, boundaryKey)
		}
	})
}

func TestRegisterRejectsUninitializedUploads(t *testing.T) {
	if release, err := Register(&Uploads{}); err == nil {
		_ = release()
		t.Fatal("empty upload service registered")
	}
}

func TestAllCompletedInvalidInput(t *testing.T) {
	// 无效输入应直接返回，无需查询仓储。
	var uploads Uploads
	for _, tc := range []struct {
		scene string
		ids   []uint64
	}{
		{" \t", []uint64{1}},
		{"files", nil},
		{"files", []uint64{1, 0}},
	} {
		if got, err := uploads.AllCompleted(t.Context(), tc.scene, tc.ids); got || err != nil {
			t.Errorf("AllCompleted(%q, %v) = %v, %v", tc.scene, tc.ids, got, err)
		}
	}
}

func TestAllCompleted(t *testing.T) {
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			ctx := t.Context()
			db := testdb.Open(t, backend)
			store, err := repo.Open(ctx, repo.StoreConfig{Backend: backend, Client: testdb.Client{Primary: db}})
			if err != nil {
				t.Fatal(err)
			}
			rows := []model.Record{
				{ID: 1, ObjectKey: "one", Scene: "Files", Status: model.Success},
				{ID: 2, ObjectKey: "two", Scene: "FILES", Status: model.Success},
				{ID: 3, ObjectKey: "three", Scene: "files", Status: model.Pending},
				{ID: 4, ObjectKey: "four", Scene: "other", Status: model.Success},
				{ID: 5, ObjectKey: "five", Scene: "files", Status: 9},
				{ID: 6, ObjectKey: "six", Scene: "İmages", Status: model.Success},
			}
			if err := store.Create(ctx, rows); err != nil {
				t.Fatal(err)
			}
			// 未配置当前上传场景，仍可按记录中的场景校验历史文件。
			uploads := Uploads{store: store}
			for _, tc := range []struct {
				scene string
				ids   []uint64
				want  bool
			}{
				{"files", []uint64{2, 1, 2}, true},
				{"files", []uint64{1, 1, 99}, false},
				{"files", []uint64{1, 3}, false},
				{"files", []uint64{1, 4}, false},
				{"files", []uint64{5}, false},
				{"files ", []uint64{1}, false},
				{"images", []uint64{6}, true},
				{"fileſ", []uint64{1}, false},
			} {
				if got, err := uploads.AllCompleted(ctx, tc.scene, tc.ids); err != nil || got != tc.want {
					t.Errorf("AllCompleted(%q, %v) = %v, %v，预期 %v, nil", tc.scene, tc.ids, got, err, tc.want)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if got, err := uploads.AllCompleted(canceled, "files", []uint64{1}); got || !errors.Is(err, context.Canceled) {
				t.Errorf("取消查询得到 %v, %v，预期 false, context.Canceled", got, err)
			}
		})
	}
}

func TestUploadURLMaps(t *testing.T) {
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			ctx := t.Context()
			db := testdb.Open(t, backend)
			store, err := repo.Open(ctx, repo.StoreConfig{Backend: backend, Client: testdb.Client{Primary: db}})
			if err != nil {
				t.Fatal(err)
			}
			rows := []model.Record{
				{ID: 1, BucketType: "public", ObjectKey: "a/origin #1", WatermarkObjectKey: "a/display ?1", Status: model.Pending},
				{ID: 2, BucketType: "private", ObjectKey: "b/origin #2", WatermarkObjectKey: "b/display ?2", Status: model.Success},
				{ID: 3, BucketType: "public", ObjectKey: "plain.jpg", Status: model.Success},
			}
			if err := store.Create(ctx, rows); err != nil {
				t.Fatal(err)
			}
			private, err := ossx.New(ossx.Config{
				Name:            "url-map",
				Region:          "cn-shanghai",
				Bucket:          "private-bucket",
				Endpoint:        "http://127.0.0.1",
				AccessKeyID:     "test-id",
				AccessKeySecret: "test-secret",
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = private.Close() })
			const cdn = "https://cdn.example.com/assets/"
			uploads := Uploads{
				store: store,
				config: Config{
					PublicBucketCDN:         cdn,
					PrivateBucketURLExpires: new(90 * time.Second),
				},
				buckets: bucketServices{private: &Service{oss: private}},
			}
			ids := []uint64{2, 1, 3, 1, 99}
			var queries int
			if err := db.Callback().Query().After("gorm:query").Register("test:count_url_map_queries", func(*gorm.DB) {
				queries++
			}); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name       string
				resolve    func(context.Context, []uint64) (map[uint64]string, error)
				publicURL  string
				privateKey string
			}{
				{"display", uploads.DisplayURLMap, cdn + "a/display%20%3F1", "b/display ?2"},
				{"origin", uploads.OriginURLMap, cdn + "a/origin%20%231", "b/origin #2"},
				{"display on master", uploads.DisplayURLMapOnMaster, cdn + "a/display%20%3F1", "b/display ?2"},
				{"origin on master", uploads.OriginURLMapOnMaster, cdn + "a/origin%20%231", "b/origin #2"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					before := queries
					got, err := tc.resolve(ctx, ids)
					if err != nil || len(got) != 3 || got[1] != tc.publicURL || got[3] != cdn+"plain.jpg" {
						t.Fatalf("批量结果数量或公有 URL 不符，返回 %d 项，错误 %v", len(got), err)
					}
					u, err := url.Parse(got[2])
					if err != nil || u.Path != "/private-bucket/"+tc.privateKey || u.Query().Get("x-oss-expires") != "90" || u.Query().Get("x-oss-signature") == "" {
						t.Fatal("私有 URL 的对象路径、签名或有效期不符")
					}
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					if empty, err := tc.resolve(canceled, nil); err != nil || empty == nil || len(empty) != 0 {
						t.Fatalf("空输入应直接返回空 map，错误 %v", err)
					}
					if reads := queries - before; reads != 1 {
						t.Fatalf("批量取地址及空输入合计查询了 %d 次，预期一次", reads)
					}
					if partial, err := tc.resolve(canceled, ids); !errors.Is(err, context.Canceled) || partial != nil {
						t.Fatalf("查询取消时应返回 nil 和原错误，得到 %v", err)
					}
				})
			}
			if err := private.Close(); err != nil {
				t.Fatal(err)
			}
			if partial, err := uploads.DisplayURLMap(ctx, ids); !errors.Is(err, ossx.ErrClosed) || partial != nil {
				t.Fatalf("私有签名失败时应整体返回错误，得到 %v", err)
			}
		})
	}
}

func TestUploadsWithLaggingReplica(t *testing.T) {
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			ctx := t.Context()
			primaryDB := testdb.Open(t, backend)
			replicaDB := testdb.Open(t, backend)
			store, err := repo.Open(ctx, repo.StoreConfig{
				Backend: backend,
				Client: testdb.Client{
					Primary: primaryDB,
					Replica: replicaDB,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			replica, err := repo.Open(ctx, repo.StoreConfig{
				Backend: backend,
				Client:  testdb.Client{Primary: replicaDB},
			})
			if err != nil {
				t.Fatal(err)
			}
			const cdn = "https://cdn.example.com/assets"
			uploads := Uploads{
				store:  store,
				config: Config{PublicBucketCDN: cdn},
			}
			records := []model.Record{
				{
					ObjectKey:          "files/lagged.json",
					WatermarkObjectKey: "files/lagged-display.json",
					Scene:              "files",
					BucketType:         "public",
					Status:             model.Pending,
				},
			}
			if err := store.Create(ctx, records); err != nil {
				t.Fatal(err)
			}
			id := records[0].ID
			req := model.OSSUploadConfirmRequest{Items: []model.OSSUploadConfirmItemRequest{{ID: id}}}
			if err := uploads.Confirm(ctx, req); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Confirm before replication = %v, want %v", err, ErrNotFound)
			}
			ids := []uint64{id}
			if got, err := uploads.DisplayURLMap(ctx, ids); err != nil || got == nil || len(got) != 0 {
				t.Fatalf("从库尚无记录时应返回空 map，错误 %v", err)
			}
			if got, err := uploads.DisplayURLMapOnMaster(ctx, ids); err != nil || len(got) != 1 || got[id] != cdn+"/files/lagged-display.json" {
				t.Fatalf("从库尚无记录时应从主库返回水印地址，得到 %v, %v", got, err)
			}
			if got, err := uploads.OriginURLMapOnMaster(ctx, ids); err != nil || len(got) != 1 || got[id] != cdn+"/files/lagged.json" {
				t.Fatalf("从库尚无记录时应从主库返回原始地址，得到 %v, %v", got, err)
			}
			// 分别推进待确认记录和确认状态，模拟两次写入之间的复制延迟。
			if err := replica.Create(ctx, records); err != nil {
				t.Fatal(err)
			}
			if err := store.Confirm(ctx, records); err != nil {
				t.Fatal(err)
			}
			if got, err := uploads.AllCompleted(ctx, "files", []uint64{id}); got || err != nil {
				t.Fatalf("从库仍为待确认时 AllCompleted 得到 %v, %v", got, err)
			}
			want := cdn + "/files/lagged-display.json"
			if got, err := uploads.DisplayURLMap(ctx, ids); err != nil || got[id] != want {
				t.Fatalf("从库记录待确认时仍应返回对象地址，错误 %v", err)
			}
			if err := replica.Confirm(ctx, records); err != nil {
				t.Fatal(err)
			}
			if got, err := uploads.AllCompleted(ctx, "files", ids); err != nil || !got {
				t.Fatalf("从库确认状态追平后 AllCompleted 得到 %v, %v", got, err)
			}
		})
	}
}
