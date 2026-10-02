package sessionwd

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestCommandUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"--user", ""},
		{"--user", "x", "extra"},
		{"--user", "x", "--log-level", "loud"},
		{"--bogus"},
		{"--user", "bad name"},
	}
	for _, args := range cases {
		err := Command(context.Background(), args, io.Discard)
		var ue UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: 想要 UsageError, got %v", args, err)
		}
	}
}
