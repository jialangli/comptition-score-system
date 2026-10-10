package model

import "testing"

// 场次类型只有三值；加时赛与重赛同属「非正式场次」（机制相同，只是启用场景不同）。
func TestSlotType(t *testing.T) {
	cases := []struct {
		t       SlotType
		valid   bool
		extra   bool
		display string
	}{
		{SlotNormal, true, false, "正式场次"},
		{SlotExtra, true, true, "加时赛（独立场次）"},
		{SlotRematch, true, true, "重赛（独立场次）"},
		{SlotType(""), false, false, ""},
		{SlotType("weird"), false, false, "weird"},
	}
	for _, c := range cases {
		if got := c.t.Valid(); got != c.valid {
			t.Errorf("%q.Valid() = %v，期望 %v", c.t, got, c.valid)
		}
		if got := c.t.Extra(); got != c.extra {
			t.Errorf("%q.Extra() = %v，期望 %v", c.t, got, c.extra)
		}
		if got := c.t.Display(); got != c.display {
			t.Errorf("%q.Display() = %q，期望 %q", c.t, got, c.display)
		}
	}
}
