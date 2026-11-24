package ossboot_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/aliyunossboot"
	"github.com/bpcoder16/baize/bootstrap/aliyunstsboot"
	"github.com/bpcoder16/baize/bootstrap/mysqlboot"
	"github.com/bpcoder16/baize/bootstrap/ossboot"
	"github.com/bpcoder16/baize/bootstrap/postgresqlboot"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/internal/testdb"
	model "github.com/bpcoder16/baize/model/oss"
	repo "github.com/bpcoder16/baize/repository/oss"
	upload "github.com/bpcoder16/baize/service/oss"
	"github.com/bpcoder16/pixiu/infra/mysqlx"
	"github.com/bpcoder16/pixiu/infra/postgresqlx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestPrepareRequiresDeclaredDatabase(t *testing.T) {
	cfg := ossboot.Config{Backend: constant.MySQL, DatabaseName: "uploads"}
	var stack lifecycle.Stack
	err := ossboot.Prepare(cfg).Init(t.Context(), t.TempDir(), &stack)
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared database: %v", err)
	}
}

// 每种方言使用独立测试库和进程，走真实的资源装配、迁表、上传服务调用和解绑。
func TestPrepareUpload(t *testing.T) {
	backend := constant.Backend(os.Getenv("BAIZE_OSS_BOOT_CHILD"))
	if backend == "" {
		for _, b := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
			t.Run(string(b), func(t *testing.T) {
				dir := t.TempDir()
				db := testdb.Create(t, b)
				var host, port, name, user, password string
				if c := db.MySQL; c != nil {
					var err error
					host, port, err = net.SplitHostPort(c.Addr)
					if err != nil {
						t.Fatal(err)
					}
					name, user, password = c.DBName, c.User, c.Passwd
				} else {
					c := db.PostgreSQL
					host, port, name, user, password = c.Host, strconv.Itoa(int(c.Port)), c.Database, c.User, c.Password
				}
				writeConfig(t, dir, string(b)+".default.yaml", fmt.Sprintf("master:\n  host: %q\n  port: %s\ndatabase: %q\nusername: %q\npassword: %q\n", host, port, name, user, password))
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPrepareUpload$")
				cmd.Env = append(os.Environ(), "BAIZE_OSS_BOOT_CHILD="+string(b), "BAIZE_OSS_BOOT_DIR="+dir)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	dir := os.Getenv("BAIZE_OSS_BOOT_DIR")
	var heads atomic.Int32
	bothReadPending := make(chan struct{})
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"Credentials":{"AccessKeyId":"test","AccessKeySecret":"test","SecurityToken":"token","Expiration":"2030-01-01T00:00:00Z"}}`)
		case http.MethodHead:
			// 两批都已预读待确认记录，再让它们竞争主库更新。
			if heads.Add(1) == 2 {
				close(bothReadPending)
			}
			select {
			case <-bothReadPending:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Length", "7")
		case http.MethodPut:
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected cloud method %s", r.Method)
		}
	}))
	defer cloud.Close()
	cfg := ossboot.Config{
		Backend:           backend,
		DatabaseName:      "default",
		STSName:           "issuer",
		PublicBucketName:  "assets",
		PrivateBucketName: "assets",
	}
	buckets := []string{"assets"}
	kind := "public"
	dbResource := mysqlboot.Instances("default")
	if backend == constant.PostgreSQL {
		dbResource = postgresqlboot.Instances("default")
		cfg.PrivateBucketName = "documents"
		buckets = append(buckets, "documents")
		kind = "private"
	}
	writeConfig(t, dir, "app.yaml", fmt.Sprintf("env:\n  appName: uploads\n  runMode: test\n  localIP: 127.0.0.1\n  timeLocation: UTC\nlog:\n  format: json\n  hookStd: false\n  dir: %q\n", dir))
	writeConfig(t, dir, string(backend)+"-migrate.default.yaml", "enabled: true\n")
	for _, name := range buckets {
		writeConfig(t, dir, "aliyunossx."+name+".yaml", fmt.Sprintf("region: cn-shanghai\nbucket: %s\naccessKeyID: example\naccessKeySecret: example\nendpoint: %q\n", name, cloud.URL))
	}
	writeConfig(t, dir, "aliyunstsx.issuer.yaml", fmt.Sprintf("region: cn-shanghai\naccessKeyID: example\naccessKeySecret: example\nendpoint: %q\n", cloud.URL))
	settings := "stsRoleARN: acs:ram::123456789012:role/upload\npublicBucketCDN: https://cdn.example.com/assets\nprivateBucketURLExpires: 90s\nscenes:\n  files:\n    bucketType: " + kind + "\n    targetDir: files\n    allowedExts: [.json]\n    maxBatchCount: 2\n    maxSize: 1024\n"
	writeConfig(t, dir, "baize.oss.yaml", settings)

	prepare := ossboot.Prepare(cfg)
	plan := bootstrap.Plan{
		Resources: []bootstrap.Resource{dbResource, aliyunossboot.Instances(buckets...), aliyunstsboot.Instances("issuer")},
		Prepare: []bootstrap.Step{
			{Name: "check-settings", Init: func(ctx context.Context, _ string, stack *lifecycle.Stack) error {
				other := t.TempDir()
				if err := prepare.Init(ctx, other, stack); err == nil || !strings.Contains(err.Error(), "baize.oss.yaml") {
					return fmt.Errorf("missing scene file: %v", err)
				}
				database := testDatabase(backend)
				if store, err := repo.OpenExisting(ctx, repo.StoreConfig{Backend: backend, Client: database}); err == nil || store != nil {
					return fmt.Errorf("missing upload table accepted")
				}
				if kind == "public" {
					writeConfig(t, other, string(backend)+"-migrate.default.yaml", "enabled: true\n")
					writeConfig(t, other, "baize.oss.yaml", strings.Replace(settings, "bucketType: public", "bucketType: private", 1))
					if err := prepare.Init(ctx, other, stack); err == nil || !strings.Contains(err.Error(), "files") {
						return fmt.Errorf("private scene on single bucket: %v", err)
					}
					if database.MasterDB(ctx).Migrator().HasTable("baize_oss_upload_records") {
						return fmt.Errorf("upload table created for invalid bucket binding")
					}
				}
				return nil
			}},
			prepare,
			{Name: "verify", Init: func(ctx context.Context, _ string, _ *lifecycle.Stack) error {
				uploads := upload.Default()
				if uploads == nil {
					return errors.New("OSS upload service not registered")
				}
				token, err := uploads.CreateToken(ctx, model.OSSUploadTokenRequest{
					Scene: "files",
					Files: []model.OSSUploadTokenFileRequest{
						{ClientFileID: "f", FileExt: "json", FileSize: 5},
						{ClientFileID: "g", FileExt: "json", FileSize: 7},
					},
				})
				if err != nil {
					return err
				}
				if len(token.Items) != 2 || token.Items[0].ID == 0 || token.SecurityToken != "token" || token.BucketName != cfg.PrivateBucketName {
					return fmt.Errorf("unexpected token response")
				}
				item := token.Items[0]
				first := model.OSSUploadConfirmItemRequest{ID: item.ID}
				second := model.OSSUploadConfirmItemRequest{ID: token.Items[1].ID}
				confirm := model.OSSUploadConfirmRequest{Items: []model.OSSUploadConfirmItemRequest{first, second}}
				reversed := model.OSSUploadConfirmRequest{Items: []model.OSSUploadConfirmItemRequest{second, first}}
				raceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- uploads.Confirm(raceCtx, reversed) }()
				errA := uploads.Confirm(raceCtx, confirm)
				errB := <-done
				if !(errA == nil && errors.Is(errB, upload.ErrState) || errB == nil && errors.Is(errA, upload.ErrState)) {
					return fmt.Errorf("并发确认应一次成功、一次返回 ErrState，得到 %v 和 %v", errA, errB)
				}
				if err = uploads.Confirm(ctx, confirm); !errors.Is(err, upload.ErrState) {
					return fmt.Errorf("repeat confirmation: got %v, want %v", err, upload.ErrState)
				}
				database := testDatabase(backend)
				store, err := repo.OpenExisting(ctx, repo.StoreConfig{Backend: backend, Client: database})
				if err != nil {
					return err
				}
				rows, err := store.FindOnMaster(ctx, []uint64{item.ID})
				if err != nil || len(rows) != 1 {
					return fmt.Errorf("read confirmed upload: %v, rows %d", err, len(rows))
				}
				row := rows[0]
				if row.FileSize != 5 || row.ActualFileSize == nil || *row.ActualFileSize != 7 || row.WatermarkObjectKey != "" || row.Status != model.Success || row.ObjectKey != item.ObjectKey {
					return fmt.Errorf("unexpected confirmed row: %+v", row)
				}
				db := database.MasterDB(ctx)
				if db.Migrator().HasColumn("baize_oss_upload_records", "portal") || db.Migrator().HasColumn("baize_oss_upload_records", "operator_user_id") || db.Migrator().HasTable("common_oss_upload_records") {
					return fmt.Errorf("unexpected legacy schema")
				}
				origins, err := uploads.OriginURLMap(ctx, []uint64{item.ID})
				if err != nil {
					return err
				}
				raw := origins[item.ID]
				u, err := url.Parse(raw)
				if err != nil {
					return err
				}
				if kind == "public" && raw != "https://cdn.example.com/assets/"+item.ObjectKey || kind == "private" && u.Query().Get("x-oss-expires") != "90" {
					return fmt.Errorf("incorrect download URL for %s", kind)
				}
				var body bytes.Buffer
				form := multipart.NewWriter(&body)
				part, err := form.CreateFormFile("file", "example.json")
				if err != nil {
					return err
				}
				if _, err = io.WriteString(part, "{}"); err != nil {
					return err
				}
				if err = form.Close(); err != nil {
					return err
				}
				files, err := multipart.NewReader(&body, form.Boundary()).ReadForm(1024)
				if err != nil {
					return err
				}
				defer files.RemoveAll()
				direct, err := uploads.DirectUpload(ctx, "files", files.File["file"][0])
				if err != nil {
					return err
				}
				rows, err = store.FindOnMaster(ctx, []uint64{direct.ID})
				if err != nil || len(rows) != 1 {
					return fmt.Errorf("read direct upload: %v, rows %d", err, len(rows))
				}
				row = rows[0]
				if row.Status != model.Success || row.BucketType != kind || row.FileSize != 2 {
					return fmt.Errorf("unexpected direct upload row: %+v", row)
				}
				t.Run("并发登记", func(t *testing.T) {
					testConcurrentRegistration(t, uploads, store, files.File["file"][0])
				})
				if alterErr := db.Exec("ALTER TABLE baize_oss_upload_records DROP COLUMN watermark_object_key").Error; alterErr != nil {
					return alterErr
				}
				if existing, openErr := repo.OpenExisting(ctx, repo.StoreConfig{Backend: backend, Client: database}); openErr == nil || existing != nil {
					return fmt.Errorf("incomplete upload table accepted")
				}
				return nil
			}},
		},
	}
	if err := bootstrap.Run(t.Context(), filepath.Join(dir, "app.yaml"), plan); err != nil {
		t.Fatal(err)
	}
	if upload.Default() != nil {
		t.Fatal("upload service still registered after shutdown")
	}
}

func testConcurrentRegistration(t *testing.T, uploads *upload.Uploads, store *repo.Store, file *multipart.FileHeader) {
	check := func(t *testing.T, id uint64, key string, size int64, status uint8) {
		t.Helper()
		rows, err := store.FindOnMaster(t.Context(), []uint64{id})
		if err != nil || len(rows) != 1 || rows[0].ObjectKey != key || rows[0].FileSize != size || rows[0].Status != status {
			t.Fatalf("返回的 ID %d 与预期对象 %q、大小 %d、状态 %d 不符，得到 %+v，错误 %v", id, key, size, status, rows, err)
		}
	}
	for range 4 {
		t.Run("CreateToken", func(t *testing.T) {
			t.Parallel()
			req := model.OSSUploadTokenRequest{
				Scene: "files",
				Files: []model.OSSUploadTokenFileRequest{
					{ClientFileID: "a", FileExt: "json", FileSize: 3},
					{ClientFileID: "b", FileExt: "json", FileSize: 5},
				},
			}
			token, err := uploads.CreateToken(t.Context(), req)
			if err != nil || len(token.Items) != len(req.Files) {
				t.Fatalf("批量申请返回 %d 个文件，错误 %v", len(token.Items), err)
			}
			for i, item := range token.Items {
				if item.ClientFileID != req.Files[i].ClientFileID {
					t.Fatalf("文件标识得到 %q，预期 %q", item.ClientFileID, req.Files[i].ClientFileID)
				}
				check(t, item.ID, item.ObjectKey, req.Files[i].FileSize, model.Pending)
			}
		})
		t.Run("DirectUpload", func(t *testing.T) {
			t.Parallel()
			direct, err := uploads.DirectUpload(t.Context(), "files", file)
			if err != nil {
				t.Fatal(err)
			}
			check(t, direct.ID, direct.ObjectKey, file.Size, model.Success)
		})
	}
}

func writeConfig(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func testDatabase(backend constant.Backend) repo.DBClient {
	if backend == constant.MySQL {
		return mysqlx.Named("default")
	}
	return postgresqlx.Named("default")
}
