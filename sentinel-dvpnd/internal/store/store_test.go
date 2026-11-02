package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
)

const releaseDelay = 300 * time.Millisecond

func TestOpen_WritersWaitForBusyTimeout(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "store.db")

	dbA, err := store.Open(path)
	require.NoError(t, err)

	sqlA, err := dbA.DB()
	require.NoError(t, err)

	t.Cleanup(func() { _ = sqlA.Close() })

	dbB, err := store.Open(path)
	require.NoError(t, err)

	sqlB, err := dbB.DB()
	require.NoError(t, err)

	t.Cleanup(func() { _ = sqlB.Close() })

	require.NoError(t, dbA.Exec("CREATE TABLE things (id INTEGER PRIMARY KEY, name TEXT)").Error)

	conn, err := sqlA.Conn(t.Context())
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	_, err = conn.ExecContext(t.Context(), "BEGIN IMMEDIATE")
	require.NoError(t, err)

	attempted := make(chan struct{})
	released := make(chan error, 1)

	go func() {
		<-attempted

		timer := time.NewTimer(releaseDelay)
		defer timer.Stop()

		<-timer.C

		_, err := conn.ExecContext(t.Context(), "COMMIT")
		released <- err
	}()

	close(attempted)

	err = dbB.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Raw("SELECT COUNT(*) FROM things").Scan(&count).Error; err != nil {
			return err
		}

		return tx.Exec("INSERT INTO things (name) VALUES (?)", "second").Error
	})

	require.NoError(t, <-released)
	require.NoError(t, err)

	var count int64
	require.NoError(t, dbB.Raw("SELECT COUNT(*) FROM things").Scan(&count).Error)
	require.Equal(t, int64(1), count)
}
