package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
)

const (
	busyTimeoutMillis = "5000"
	maxOpenConns      = 1
	readOnlyParams    = "mode=ro&_pragma=busy_timeout(" + busyTimeoutMillis + ")"
	readWriteParams   = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(" + busyTimeoutMillis +
		")&_pragma=foreign_keys(1)&_txlock=immediate"
)

func Migrate(ctx context.Context, db *gorm.DB, models ...any) error {
	db = db.WithContext(ctx)
	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("migrating store: %w", err)
	}

	return nil
}

func Open(path string) (*gorm.DB, error) {
	return open(path, readWriteParams)
}

func OpenReadOnly(path string) (*gorm.DB, error) {
	return open(path, readOnlyParams)
}

func WrapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %w", daemonv1.ErrNotFound, err)
	}

	return err
}

func open(path, query string) (*gorm.DB, error) {
	db, err := gorm.Open(gormlite.Open("file:"+path+"?"+query), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("resolving sql db: %w", err)
	}

	sqlDB.SetMaxOpenConns(maxOpenConns)

	return db, nil
}
