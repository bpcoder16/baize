package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	repo "github.com/bpcoder16/baize/repository/outbox"
	outbox "github.com/bpcoder16/baize/service/outbox"
	"gorm.io/gorm"
)

// MySQL 在 UTC 连接下保留夏令时回拨两侧不同的时间点；Location 只控制返回表示。
func TestMySQLTimestampDSTAndConnections(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range []*time.Location{time.UTC, shanghai} {
		t.Run(connection.String(), func(t *testing.T) {
			base, db := liveStore(t, constant.MySQL, connection)
			cfg := *base.TestConfig()
			cfg.Location = newYork
			store, err := repo.Open(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			instants := []time.Time{
				time.Date(2030, 11, 3, 5, 30, 0, 123456000, time.UTC),
				time.Date(2030, 11, 3, 6, 30, 0, 123456000, time.UTC),
			}
			for _, instant := range instants {
				value := task()
				value.ExpiresAt = instant.In(newYork)
				id := enqueueTask(t, store, db, value)
				record, err := store.TestReadRecord(context.Background(), id)
				if err != nil || record.ExpiresAt == nil || !record.ExpiresAt.Equal(instant) || record.ExpiresAt.Location() != newYork {
					t.Fatalf("回拨时刻往返失败: %+v %v", record, err)
				}
				if record.ExpiresAt.Format("15:04") != "01:30" {
					t.Fatalf("预期回拨两侧相同墙上时间: %v", record.ExpiresAt)
				}
				if time.Since(record.CreatedAt).Abs() > 10*time.Second {
					t.Fatalf("数据库时钟发生偏移: %v", record.CreatedAt)
				}
			}
		})
	}
}

func TestTimestampExpirationRange(t *testing.T) {
	eachDatabase(t, func(t *testing.T, store *repo.Store, db *gorm.DB) {
		for _, tc := range []struct {
			id    string
			value time.Time
			valid bool
		}{
			{id: "lower", value: time.Unix(1, 123456000), valid: true},
			{id: "upper", value: time.Unix(2147483647, 123456000), valid: true},
			{id: "before", value: time.Unix(0, 0)},
			{id: "after", value: time.Unix(2147483648, 0)},
		} {
			t.Run(tc.id, func(t *testing.T) {
				value := task()
				value.ExpiresAt = tc.value.In(time.FixedZone("+08", 8*3600))
				var id int64
				err := db.Transaction(func(tx *gorm.DB) error {
					var err error
					id, err = store.InsertTx(tx, value)
					return err
				})
				if store.TestConfig().Backend == constant.MySQL && !tc.valid {
					if !errors.Is(err, outbox.ErrInvalidArgument) {
						t.Fatalf("MySQL 越界时间必须明确拒绝: %v", err)
					}
					if id != 0 {
						t.Fatalf("越界任务不得返回 ID: %d", id)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				record, err := store.TestReadRecord(context.Background(), id)
				if err != nil || record.ExpiresAt == nil || !record.ExpiresAt.Equal(tc.value) {
					t.Fatalf("边界时刻未保留: %+v %v", record, err)
				}
			})
		}
	})
}
