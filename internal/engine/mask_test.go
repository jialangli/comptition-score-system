package engine

import "testing"

func TestMaskPerson(t *testing.T) {
	cases := []struct{ in, want string }{
		{"张一", "张*"},
		{"程俊淇", "程**"},
		{"张三丰", "张**"},
		{"欧阳娜娜", "欧**"}, // 不论多长都只 2 个 *，避免 * 数量泄露姓名长度
		{"张", "张"},      // 单字保留：脱掉就无法辨识
		{"A", "A"},
		{"", ""},
		{"   ", ""},
		{"  张一  ", "张*"}, // 前后空白先裁掉
		{"范冰冰", "范**"},
	}
	for _, c := range cases {
		if got := MaskPerson(c.in); got != c.want {
			t.Errorf("MaskPerson(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestMaskMembers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"张一 / 李二", "张* / 李*"},
		{"韩五、杨六、朱七", "韩* / 杨* / 朱*"},
		{"王小明,李小红，张小三|赵小四", "王** / 李** / 张** / 赵**"},
		{"程俊淇", "程**"},
		{"张一/李二", "张* / 李*"}, // 无空格
		{"周吴郑 / ", "周**"},    // 末尾空元素丢弃
		{" / 、 , ， | ", "—"}, // 全是分隔符 → 占位符
		{"", "—"},
		{"   ", "—"},
		{"张一　李二", "张**"}, // 全角空格不是分隔符 → 整体被当成一个名字
	}
	for _, c := range cases {
		if got := MaskMembers(c.in); got != c.want {
			t.Errorf("MaskMembers(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestMaskCoach(t *testing.T) {
	cases := []struct{ in, want string }{
		{"张老师", "张**"},
		{"", "—"},
		{"  ", "—"},
	}
	for _, c := range cases {
		if got := MaskCoach(c.in); got != c.want {
			t.Errorf("MaskCoach(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestMaskNeverLeaksFullName(t *testing.T) {
	// 合规底线：多字姓名必须只剩首字，且 * 的数量不随姓名长度增长
	names := []string{"张一", "程俊淇", "欧阳娜娜", "Smith", "范冰冰同学"}
	for _, n := range names {
		got := []rune(MaskPerson(n))
		want := []rune(n)

		if len(got) == 0 || got[0] != want[0] {
			t.Errorf("MaskPerson(%q) = %q，应保留首字", n, string(got))
		}
		stars := 0
		for _, r := range got[1:] {
			if r != '*' {
				t.Errorf("MaskPerson(%q) = %q，首字之后应全为 *", n, string(got))
				break
			}
			stars++
		}
		wantStars := len(want) - 1
		if wantStars > 2 {
			wantStars = 2
		}
		if stars != wantStars {
			t.Errorf("MaskPerson(%q) 的 * 数量 = %d，期望 %d", n, stars, wantStars)
		}
	}

	if m := MaskMembers("程俊淇 / 欧阳娜娜"); m != "程** / 欧**" {
		t.Errorf("MaskMembers 结果异常：%q", m)
	}
}
