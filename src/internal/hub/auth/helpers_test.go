package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

var testStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func newClock() *clock.Fake { return clock.NewFake(testStart) }

// 低成本参数，仅用于测试。
var fastParams = Params{Memory: 8, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}
