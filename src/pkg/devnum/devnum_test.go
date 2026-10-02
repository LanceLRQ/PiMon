package devnum

import "testing"

func TestDecode(t *testing.T) {
	cases := []struct {
		dev          uint64
		major, minor uint32
	}{
		{0x0d40, 13, 64},         // /dev/input/event0
		{0x0d00 | 0xc8, 13, 200}, // minor 超过 64+31
		{0x0103, 1, 3},           // /dev/null
		{0x10300 | 0x12000000, 259, 0x12000},
	}
	for _, c := range cases {
		major, minor := Decode(c.dev)
		if major != c.major || minor != c.minor {
			t.Errorf("Decode(%#x) = %d:%d, want %d:%d", c.dev, major, minor, c.major, c.minor)
		}
	}
}
