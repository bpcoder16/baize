package outbox

import (
	"context"

	model "github.com/bpcoder16/baize/model/outbox"
	"gorm.io/gorm"
)

var InCleanupWindow = inCleanupWindow
var DeleteFinishedBatch = (*Store).deleteFinishedBatch

func (s *Store) TestConfig() *StoreConfig                          { return &s.cfg }
func (s *Store) TestSession(ctx context.Context) (*gorm.DB, error) { return s.master(ctx) }

func (s *Store) TestReadRecord(ctx context.Context, id int64) (model.Record, error) {
	db, err := s.master(ctx)
	if err != nil {
		return model.Record{}, err
	}
	return s.takeRecord(db.Where("id = ?", id))
}
