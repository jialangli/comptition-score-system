package api_test

// ============================================================================
// 身份维度：X-Operator 请求头的解码
//
// 背景（实测踩到的坑）：HTTP 头值只能是 ByteString（0x00–0xFF），中文名字
// 没法直接放进头里 —— 前端 fetch 会在发请求之前就抛
//   TypeError: ... The character at index 0 has a value of 24037 which is greater than 255
// 也就是说，前端若直接 `headers['X-Operator'] = '工作人员'`，**整条请求发不出去**，
// 而不是「后端收到乱码」。
//
// 因此约定：前端发 percent-encoding，后端 CurrentUser 还原一次。
// 这个用例把两侧的约定钉住 —— 谁改掉其中一半，这里就会红。
// ============================================================================

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/api"
)

func TestOperatorHeaderIsDecoded(t *testing.T) {
	cases := []struct {
		name string
		raw  string // 请求头原样发过来的值
		want string // 期望落进审计的操作人
	}{
		{"不带该头 → 兜底文案", "", "运营（未声明）"},
		{"前端 encodeURIComponent 出来的中文名", "%E5%BC%A0%E4%BC%9F", "张伟"},
		{"curl 手敲的原生 UTF-8（无 % ，原样保留）", "张伟", "张伟"},
		{"空格也要编码", "%E5%BC%A0%20%E4%BC%9F", "张 伟"},
		{"只发空格 → 等同没发", "   ", "运营（未声明）"},
		{"编码后前后带空格", "  %E5%BC%A0%E4%BC%9F  ", "张伟"},
		{"PathUnescape 语义：'+' 就是加号，不是空格", "A+B", "A+B"},
		{"编码后的加号", "A%2BB", "A+B"},
		{"纯 ASCII 工号", "u10086", "u10086"},
		{"畸形的 % 序列按原文保留（宁愿留痕难看，也不要整个请求 400）", "%zz", "%zz"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			handler := api.CurrentUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = api.Operator(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
			if c.raw != "" {
				req.Header.Set(api.OperatorHeader, c.raw)
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)

			if got != c.want {
				t.Fatalf("X-Operator=%q 解出来 %q，期望 %q", c.raw, got, c.want)
			}
		})
	}
}

// TestOperatorHeaderRoundTrip 把「前端编码 → 后端解码」当成一对来验：
// 只改前端（比如把 encodeURIComponent 去掉）或只改后端，这里都会红。
func TestOperatorHeaderRoundTrip(t *testing.T) {
	names := []string{"工作人员", "张伟", "李·四", "王 五", "裁判A"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			encoded := percentEncodeUTF8(name) // 等价于浏览器的 encodeURIComponent
			if !isByteString(encoded) {
				t.Fatalf("编码结果 %q 仍不是 ByteString —— 浏览器会直接拒绝这个请求头", encoded)
			}
			var got string
			handler := api.CurrentUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = api.Operator(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)
			req.Header.Set(api.OperatorHeader, encoded)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			if got != name {
				t.Fatalf("往返后 %q != %q", got, name)
			}
		})
	}
}

// percentEncodeUTF8 与 JS 的 encodeURIComponent 对齐：
// 只有 A-Za-z0-9-_.!~*'() 不转义，其余（含 +、空格、中文）一律转义。
func percentEncodeUTF8(s string) string {
	const keep = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	const hex = "0123456789ABCDEF"
	var b []byte
	for _, c := range []byte(s) {
		if indexByte(keep, c) >= 0 {
			b = append(b, c)
			continue
		}
		b = append(b, '%', hex[c>>4], hex[c&0x0f])
	}
	return string(b)
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// isByteString 模拟 fetch 规范的校验：头值里不允许出现码位 > 0xFF 的字符。
// 注意要按 rune 判断 —— Go 的 []byte 逐字节看永远是 <= 0xFF，那样这个断言会永远为真。
func isByteString(s string) bool {
	for _, r := range s {
		if r > 0xFF {
			return false
		}
	}
	return true
}
