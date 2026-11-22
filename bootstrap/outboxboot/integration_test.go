package outboxboot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/cronboot"
	"github.com/bpcoder16/baize/bootstrap/mysqlboot"
	"github.com/bpcoder16/baize/bootstrap/natsboot"
	"github.com/bpcoder16/baize/bootstrap/postgresqlboot"
	"github.com/bpcoder16/baize/constant"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	"github.com/bpcoder16/baize/service/outbox"
	"github.com/bpcoder16/pixiu/infra/cronx"
	"github.com/bpcoder16/pixiu/infra/natsx"
	"github.com/bpcoder16/pixiu/lifecycle"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/gorm"
)

// 两种方言都从隔离的空库启动，命名客户端和启动环境由独立进程隔离。
func TestOutboxPlan(t *testing.T) {
	backend := constant.Backend(os.Getenv("BAIZE_OUTBOX_TEST_CHILD"))
	if backend == "" {
		for _, b := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
			t.Run(string(b), func(t *testing.T) {
				if os.Getenv("BAIZE_OUTBOX_"+strings.ToUpper(string(b))+"_DSN") == "" {
					t.Skip("未设置集成测试数据库 DSN")
				}
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestOutboxPlan$")
				cmd.Env = append(os.Environ(), "BAIZE_OUTBOX_TEST_CHILD="+string(b), "BAIZE_OUTBOX_TEST_DIR="+prepareFixture(t, b))
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, output)
				}
			})
		}
		return
	}
	dir := os.Getenv("BAIZE_OUTBOX_TEST_DIR")
	t.Chdir(dir)
	dbResource := mysqlboot.Instances("orders")
	if backend == constant.PostgreSQL {
		dbResource = postgresqlboot.Instances("orders")
	}
	databases := make(chan *gorm.DB, 1)
	var auditID, excludedID int64
	names := []string{"events", "audit"}
	cfg := testConfig(names...)
	cfg.Backend = backend
	cfg.CronTaskEvery = 500 * time.Millisecond
	prepare := PrepareJetStream(cfg)
	names[0] = "excluded"
	// 先在独立的 Cron 清单中制造重名，随后正常 Prepare 应能登记新的 Worker。
	conflict := bootstrap.Step{
		Name: "conflicting-outbox-job",
		Init: func(ctx context.Context, dir string, stack *lifecycle.Stack) error {
			var registry cronx.HandlerRegistry
			registry.MustRegister(func(context.Context) error { return nil }, "baize.outbox.retry.nats_jetstream")
			conflictCtx, err := cronboot.Service(&registry).BeforePrepare(ctx)
			if err != nil {
				return err
			}
			if err := prepare.Init(conflictCtx, dir, stack); err == nil || !strings.Contains(err.Error(), "duplicate job") {
				return fmt.Errorf("expected duplicate Outbox job: %v", err)
			}
			return nil
		},
	}
	plan := bootstrap.Plan{
		Resources: []bootstrap.Resource{dbResource, natsboot.Instances("audit", "excluded", "events", "core")},
		Services:  []bootstrap.Service{cronboot.Service(nil)},
		Prepare: []bootstrap.Step{conflict, prepare, {Name: "enqueue", Init: func(ctx context.Context, _ string, _ *lifecycle.Stack) error {
			db, err := masterDB(ctx, backend, "orders")
			if err != nil {
				return err
			}
			for _, name := range []string{"events", "audit", "excluded"} {
				_, err = natsx.Named(name).JetStream().CreateStream(ctx, jetstream.StreamConfig{Name: "ORDERS", Subjects: []string{"orders.created"}})
				if err != nil {
					return err
				}
			}
			msg := outbox.JetStreamMessage{
				ResourceName: "audit",
				Subject:      "orders.created",
				Data:         []byte("committed"),
				Header:       map[string][]string{},
				ExpiresAt:    time.Now().Add(time.Hour),
			}
			for _, target := range []string{"unknown", "excluded"} {
				if _, err := outbox.EnqueueJetStreamTx(nil, outbox.JetStreamMessage{ResourceName: target, Subject: msg.Subject}); !errors.Is(err, outbox.ErrInvalidResource) {
					return fmt.Errorf("target %q: expected invalid resource, got %v", target, err)
				}
			}
			rollback := errors.New("rollback")
			if err = db.Transaction(func(tx *gorm.DB) error {
				if _, err := outbox.EnqueueJetStreamTx(tx, msg); err != nil {
					return err
				}
				return rollback
			}); !errors.Is(err, rollback) {
				return fmt.Errorf("expected rollback, got %v", err)
			}
			if _, err := jetStreamClients(ctx, []string{"audit", "missing"}); err == nil || !strings.Contains(err.Error(), "missing") {
				return fmt.Errorf("missing required instance: %v", err)
			}
			if _, err := jetStreamClients(ctx, []string{"audit", "core"}); err == nil || !strings.Contains(err.Error(), "core") {
				return fmt.Errorf("JetStream-disabled instance: %v", err)
			}
			// 再次启动仓储，验证重复迁移，并模拟目标被移出名单的积压任务。
			store, err := repo.Open(ctx, repo.StoreConfig{Backend: backend, MasterDB: db})
			if err != nil {
				return err
			}
			if err := db.Transaction(func(tx *gorm.DB) error {
				var enqueueErr error
				auditID, enqueueErr = outbox.EnqueueJetStreamTx(tx, msg)
				if enqueueErr != nil {
					return enqueueErr
				}
				excludedID, enqueueErr = store.InsertTx(tx, model.Task{
					HandlerType:  constant.NatsJetStream,
					ResourceName: "excluded",
					Payload:      []byte("{}"),
					ExpiresAt:    msg.ExpiresAt,
				})
				return enqueueErr
			}); err != nil {
				return err
			}
			databases <- db
			return nil
		}}},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bootstrap.Run(ctx, filepath.Join(dir, "app.yaml"), plan) }()
	var db *gorm.DB
	select {
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case db = <-databases:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !db.Migrator().HasTable("baize_outbox") {
		t.Fatal("启动应在空库创建 baize_outbox")
	}
	for _, name := range []string{"baize_outbox_ready_idx", "baize_outbox_cleanup_idx"} {
		if !db.Migrator().HasIndex("baize_outbox", name) {
			t.Fatalf("新表缺少索引 %s", name)
		}
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var rows []model.Record
		if err := db.Table("baize_outbox").Order("id").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].ID != auditID || rows[0].ResourceName != "audit" || rows[1].ID != excludedID || rows[1].ResourceName != "excluded" {
			t.Fatalf("unexpected outbox rows: %+v", rows)
		}
		if rows[0].Status == constant.OutboxSucceeded && rows[1].Status == constant.OutboxFailed {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("premature stop: %v", err)
		case <-ctx.Done():
			t.Fatal("cron did not publish")
		case <-tick.C:
		}
	}
	if err := outbox.SendJetStream(ctx, excludedID); !errors.Is(err, outbox.ErrInvalidResource) {
		t.Fatalf("terminal task %d: %v", excludedID, err)
	}
	var failedRow model.Record
	if err := db.WithContext(ctx).Table("baize_outbox").Where("id = ?", excludedID).Take(&failedRow).Error; err != nil ||
		failedRow.Attempts != 1 || failedRow.Status != constant.OutboxFailed || failedRow.FinishedAt == nil || failedRow.LastError == "" {
		t.Fatalf("terminal task %d: %+v, %v", excludedID, failedRow, err)
	}
	for name, want := range map[string]uint64{"audit": 1, "events": 0, "excluded": 0} {
		stream, err := natsx.Named(name).JetStream().Stream(ctx, "ORDERS")
		if err != nil {
			t.Fatal(err)
		}
		info, err := stream.Info(ctx)
		if err != nil || info.State.Msgs != want {
			t.Fatalf("%s: %+v, %v", name, info, err)
		}
	}
	// 两个集群接受相同 subject，按正文验证主动发送的实际目的地。
	var immediateID int64
	err := db.Transaction(func(tx *gorm.DB) error {
		var enqueueErr error
		immediateID, enqueueErr = outbox.EnqueueJetStreamTx(tx, outbox.JetStreamMessage{
			Subject:   "orders.created",
			Data:      []byte("immediate"),
			Header:    map[string][]string{},
			ExpiresAt: time.Now().Add(time.Hour),
		})
		return enqueueErr
	})
	if err != nil {
		t.Fatal(err)
	}
	var row model.Record
	if err := db.WithContext(ctx).Table("baize_outbox").Where("id = ?", immediateID).Take(&row).Error; err != nil || row.ResourceName != "events" || row.HandlerType != constant.NatsJetStream {
		t.Fatalf("persisted default: %q, %v", row.ResourceName, err)
	}
	if err := outbox.SendJetStream(ctx, immediateID); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"events": "immediate", "audit": "committed"} {
		stream, err := natsx.Named(name).JetStream().Stream(ctx, "ORDERS")
		if err != nil {
			t.Fatal(err)
		}
		msg, err := stream.GetMsg(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if name == "audit" && msg.Header.Get("Nats-Msg-Id") != strconv.FormatInt(auditID, 10) {
			t.Fatalf("Cron message ID: %v", msg.Header)
		}
		if string(msg.Data) != want {
			t.Fatalf("resource %s received %q, want %q", name, msg.Data, want)
		}
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.EnqueueJetStreamTx(nil, outbox.JetStreamMessage{ResourceName: "audit"}); !errors.Is(err, outbox.ErrHandlerNotRegistered) {
		t.Fatalf("outbox still bound: %v", err)
	}
}

// 由父进程启动 NATS 和建库，宿主设置 time.Local 时没有其他组件在运行。
func prepareFixture(t *testing.T, backend constant.Backend) string {
	t.Helper()
	dir := t.TempDir()
	conf := func(name string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name+".yaml"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	conf("app", map[string]any{"env": map[string]any{"appName": "outbox", "runMode": "test", "localIP": "127.0.0.1", "timeLocation": "UTC"}, "log": map[string]any{"format": "text", "hookStd": false}})
	conf("cron", map[string]any{"jobs": []any{}})
	conf(string(backend)+"-migrate.orders", map[string]any{"enabled": true})
	conf(string(backend)+".orders", testDatabase(t, backend))
	for _, name := range []string{"events", "audit", "excluded", "core"} {
		srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: name != "core", StoreDir: t.TempDir(), NoSigs: true})
		if err != nil {
			t.Fatal(err)
		}
		srv.Start()
		t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
		if !srv.ReadyForConnections(5 * time.Second) {
			t.Fatal("NATS not ready")
		}
		conf("nats."+name, map[string]any{"urls": []string{srv.ClientURL()}})
	}
	return dir
}

func testDatabase(t *testing.T, b constant.Backend) map[string]any {
	t.Helper()
	dsn := os.Getenv("BAIZE_OUTBOX_" + strings.ToUpper(string(b)) + "_DSN")
	var admin *sql.DB
	var user, password, host string
	var port int
	if b == constant.MySQL {
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		var portText string
		host, portText, err = net.SplitHostPort(cfg.Addr)
		if err != nil {
			t.Fatal(err)
		}
		port, err = strconv.Atoi(portText)
		if err != nil {
			t.Fatal(err)
		}
		user, password = cfg.User, cfg.Passwd
		admin, err = sql.Open("mysql", dsn)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		admin = stdlib.OpenDB(*cfg)
		host, port, user, password = cfg.Host, int(cfg.Port), cfg.User, cfg.Password
	}
	t.Cleanup(func() { admin.Close() })
	dbName := fmt.Sprintf("baize_outbox_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + dbName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE " + dbName); err != nil {
			t.Error(err)
		}
	})
	cfg := map[string]any{"master": map[string]any{"host": host, "port": port}, "database": dbName, "username": user, "password": password, "location": "UTC", "logSQL": false}
	if b == constant.PostgreSQL {
		cfg["sslMode"] = "disable"
	}
	return cfg
}
