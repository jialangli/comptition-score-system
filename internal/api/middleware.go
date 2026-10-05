package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jialangli/comptition-score-server/internal/service"
)

// Middleware 中间件签名。
type Middleware func(http.Handler) http.Handler

// Chain 按声明顺序组合中间件，第一个参数位于最外层（最先执行）。
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// statusRecorder 记录实际写出的状态码，供访问日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

// Recover 捕获 panic，避免单个请求把整个服务打挂。
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("请求发生 panic", "method", r.Method, "path", r.URL.Path, "panic", rec)
				writeJSON(w, http.StatusInternalServerError,
					Envelope{Code: CodeInternal, Message: "服务器内部错误"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LogRequests 访问日志：方法 / 路径 / 状态码 / 耗时。
func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"cost", time.Since(start).Round(time.Millisecond).String())
	})
}

// CORS 仅在开发模式放开跨域（前端单独起 dev server 时用）。
// 生产为同源部署（Go 直接 serve 前端），不需要放开。
func CORS(dev bool) Middleware {
	return func(next http.Handler) http.Handler {
		if !dev {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// OperatorHeader 客户端声明操作者的请求头。
//
// 这是**鉴权上线之前的过渡机制**，不是安全边界：任何人都能填任意名字。
// 它的价值在于让现场联调与内网演示时，审计记录里出现的是真实的人名，
// 而不是一整片「未登录」—— 后者会让「留痕」这件事在演示阶段就失去说服力。
//
// 接入「姓名 + 裁判码」双因子登录时，把本中间件的取值来源换成会话/令牌即可，
// 所有 handler 与 service 调用都不用改。
const OperatorHeader = "X-Operator"

// CurrentUser 把操作者注入 context，供 service 层审计留痕使用。
//
// 写入的是 service.WithUser 定义的 key —— 中间件与业务层必须共用同一个 key，
// 否则会出现「中间件写了一个值、service 读的是另一个 key」这种静默失效：
// 审计里全是默认值，但没有任何报错。
func CurrentUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.Header.Get(OperatorHeader))
		if name == "" {
			name = defaultOperator
		}
		ctx := service.WithUser(r.Context(), name)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// defaultOperator 未声明操作者时的兜底。
//
// 刻意不叫「未登录」：service 层已经有一个同名的兜底常量，
// 这里再给一个不同的名字，是为了在审计里能区分
// 「请求根本没带操作者」和「service 被非 HTTP 途径调用（如定时任务、测试）」。
const defaultOperator = "运营（未声明）"

// Operator 取出当前操作者。委托给 service.CurrentUser，保证取值口径一致。
func Operator(ctx context.Context) string {
	return service.CurrentUser(ctx)
}

// ============================================================================
// 当前赛事
// ============================================================================

// ContestHeader 客户端声明当前赛事的请求头。
//
// 与 X-Operator 同一套过渡机制：前端顶栏切换赛事时带上该赛事 id，
// 后端据此把本次请求的所有读写都限定在这一场赛事的数据分区内。
//
// ⚠️ 这不是安全边界 —— 任何人都能填任意赛事 id。
//    真正的隔离要靠后端鉴权（登录态 → 可访问的赛事列表）。
//    在鉴权上线前，它的作用是让多赛事功能能跑通、能联调，
//    而不是防止越权访问。
const ContestHeader = "X-Contest"

// CurrentContest 把赛事 id 注入 context，供 store 层做数据分区。
//
// 必须在业务 handler 之前执行：store 层每条 SQL 都从 ctx 取赛事，
// 中间件没跑就等于全部落到默认赛事 ct_default。
//
// 未带该头时**不报错**，而是回落到默认赛事 —— 这样现有单赛事调用方
// （以及不带头的老客户端）行为不变；多赛事客户端显式带上即可。
func CurrentContest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get(ContestHeader))
		if id == "" {
			// 不注入，让 store.CurrentContest 自己回落默认值
			next.ServeHTTP(w, r)
			return
		}
		ctx := service.WithContest(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
