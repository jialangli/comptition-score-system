// Package api 是 HTTP 层。
//
// 职责边界（架构铁律）：只做参数解析 / 校验 / 序列化，
// 不写业务逻辑，也不直接调用 store —— 必须经过 service。
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// maxBodyBytes 请求体上限（4MB），足够覆盖 xlsx 之外的常规 JSON 请求。
const maxBodyBytes = 4 << 20

// Envelope 统一响应格式：{code, message, data}。
// code = 0 表示成功；非 0 时 data 为空、message 为可读的错误说明。
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// 业务错误码。与 HTTP 状态码解耦，前端可按 code 分支处理。
const (
	CodeOK         = 0
	CodeBadRequest = 40000
	CodeNotFound   = 40400
	CodeConflict   = 40900
	CodeInternal   = 50000
)

// 成功响应约定：
//
//	POST 新建资源 → 201 Created
//	其余成功      → 200 OK
//
// 前端只需判断 `code == 0`（或 HTTP 2xx）即为成功，不必区分 200/201；
// 之所以还用 201，是为了让 curl / Postman 手工验证时一眼看出「这次是新建」。

// AppError 应用级错误：同时携带 HTTP 状态码与业务码。
type AppError struct {
	Status  int
	Code    int
	Message string
}

func (e *AppError) Error() string { return e.Message }

// NewBadRequest 400 参数问题。
func NewBadRequest(msg string) *AppError {
	return &AppError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: msg}
}

// NewNotFound 404 资源不存在。
func NewNotFound(msg string) *AppError {
	return &AppError{Status: http.StatusNotFound, Code: CodeNotFound, Message: msg}
}

// NewConflict 409 状态冲突（如重复、被引用）。
func NewConflict(msg string) *AppError {
	return &AppError{Status: http.StatusConflict, Code: CodeConflict, Message: msg}
}

func writeJSON(w http.ResponseWriter, status int, body Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("响应序列化失败", "err", err)
	}
}

// OK 输出成功响应（200）。
func OK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, Envelope{Code: CodeOK, Message: "ok", Data: data})
}

// Created 输出「资源已创建」响应（201）。
func Created(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusCreated, Envelope{Code: CodeOK, Message: "created", Data: data})
}

// Fail 把任意错误映射为标准响应。
//
// 映射顺序（自上而下，第一条命中即返回）：
//
//	AppError              → 已是 HTTP 语义，直接输出
//	service.ValidationError → 400，并把完整校验结论放进 data（前端一次标出所有问题）
//	ErrReasonRequired     → 400，缺「必须说明的原因」
//	ErrScoreSubmitted     → 409，已签字成绩禁止直接改
//	ErrChangePending      → 409，该队该轮已有待审批改分申请
//	ErrConflictRows       → 409，导入存在冲突行待裁决
//	store.Err*            → 404 / 409
//	model.FieldError      → 400，字段级
//	其它                  → 500（只写日志，不把内部细节回给客户端）
//
// 注意 ValidationError 分支回填 data：校验结论里同时含 error 与 warning，
// 是「一次报全」而不是「改一个报一个」的关键。
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		writeJSON(w, appErr.Status, Envelope{Code: appErr.Code, Message: appErr.Message})
		return
	}

	var ve *service.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusBadRequest, Envelope{
			Code:    CodeBadRequest,
			Message: ve.Error(),
			Data:    ve.Result,
		})
		return
	}

	switch {
	case errors.Is(err, service.ErrReasonRequired):
		writeJSON(w, http.StatusBadRequest, Envelope{
			Code: CodeBadRequest, Message: "该操作必须说明原因（至少 2 个字）"})
		return
	case errors.Is(err, service.ErrScoreSubmitted):
		writeJSON(w, http.StatusConflict, Envelope{
			Code: CodeConflict, Message: "成绩已提交签字，禁止直接修改，请发起改分申请"})
		return
	// 该队该轮已有一条待审批的改分申请：由 ux_scr_one_pending 部分唯一索引挡下
	//（并发下也不会双插）。这是调用方的正常误操作而非服务端故障，
	// 必须给 409 —— 给 500 会让界面弹「服务器内部错误」，运营会当成故障上报。
	case errors.Is(err, service.ErrChangePending):
		writeJSON(w, http.StatusConflict, Envelope{
			Code: CodeConflict, Message: "该队该轮已有一条待审批的改分申请，请先处理后再提交"})
		return
	case errors.Is(err, service.ErrConflictRows):
		writeJSON(w, http.StatusConflict, Envelope{
			Code: CodeConflict, Message: "导入数据存在冲突，请先人工裁决后再入库"})
		return
	case errors.Is(err, service.ErrNothingSelected):
		writeJSON(w, http.StatusBadRequest, Envelope{
			Code: CodeBadRequest, Message: "未勾选任何行，没有可入库的数据"})
		return
	case errors.Is(err, service.ErrDisputePending):
		writeJSON(w, http.StatusConflict, Envelope{
			Code: CodeConflict, Message: "该队该轮已有一条同类待裁定工单，请勿重复上报"})
		return
	case errors.Is(err, service.ErrDisputeNotPending):
		writeJSON(w, http.StatusConflict, Envelope{
			Code: CodeConflict, Message: "该工单已处理，仅待裁定工单可撤回（已裁定请走再裁定）"})
		return
	case errors.Is(err, service.ErrAdjustRequiresTarget):
		writeJSON(w, http.StatusBadRequest, Envelope{
			Code: CodeBadRequest, Message: "授权改分必须指定有效的采纳轮次（1 或 2）与目标分数"})
		return
	case errors.Is(err, service.ErrReleaseNotPublishable):
		writeJSON(w, http.StatusConflict, Envelope{
			Code: CodeConflict, Message: "该发布单元当前不可发布（须先由裁判长移交）"})
		return
	// 裁判码：三种失败必须映射到不同状态码 —— 前端靠状态码分流到
	// P1.5b（码无效）与 P1.5c（姓名不匹配）两个独立页面。
	case errors.Is(err, service.ErrRefereeNameMismatch):
		writeJSON(w, http.StatusBadRequest, Envelope{
			Code: CodeBadRequest, Message: "姓名与裁判码不匹配，请确认是否拿错了码"})
		return
	case errors.Is(err, service.ErrRefereeRevoked):
		writeJSON(w, http.StatusConflict, Envelope{
			Code: CodeConflict, Message: "该裁判码已作废，请联系工作人员重新建档"})
		return
	case errors.Is(err, service.ErrRefereeCodeInvalid):
		writeJSON(w, http.StatusNotFound, Envelope{
			Code: CodeNotFound, Message: "未找到该裁判码，请核对 6 位裁判码是否输入正确"})
		return
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, Envelope{Code: CodeNotFound, Message: "资源不存在"})
		return
	case errors.Is(err, store.ErrDuplicate):
		writeJSON(w, http.StatusConflict, Envelope{Code: CodeConflict, Message: "编号重复，请检查「一号一队」约束"})
		return
	case errors.Is(err, store.ErrInUse):
		writeJSON(w, http.StatusConflict, Envelope{Code: CodeConflict, Message: "该资源仍被引用，无法删除"})
		return
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, Envelope{Code: CodeConflict, Message: "数据状态冲突"})
		return
	}

	var fe *model.FieldError
	if errors.As(err, &fe) {
		writeJSON(w, http.StatusBadRequest, Envelope{Code: CodeBadRequest, Message: fe.Error()})
		return
	}

	slog.Error("请求处理失败", "method", r.Method, "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, Envelope{Code: CodeInternal, Message: "服务器内部错误"})
}

// decodeJSON 解析请求体：限制体积 + 拒绝未知字段。
// 拒绝未知字段是为了尽早暴露前后端字段拼写不一致，而不是静默忽略。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return NewBadRequest("请求体解析失败: " + err.Error())
	}
	return nil
}
