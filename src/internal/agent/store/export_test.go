package store

import (
	"time"

	hubstore "github.com/LanceLRQ/PiMon/src/internal/hub/store"
)

func FormatForTest(t time.Time) string { return hubstore.FormatTime(t) }
