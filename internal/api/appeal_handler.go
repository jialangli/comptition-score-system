package api

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 申述书照片（evidence.kind=appeal）
//
// 选手现场手写申述书，由裁判在裁判端拍照上传：
//
//	POST /api/v1/appeals/upload   接收 multipart 图片 → 落盘 + 登记 evidence(appeal)
//	GET  /api/v1/appeals?disputeId= 取某争议单的申述书照片（P8 裁定台用）
//	GET  /api/v1/appeals/{id}/file 出图（按 storage_url 从服务端目录读）
//
// 只收元数据 + 落盘本体，不把二进制塞进 Postgres（与 evidence 表「只存元数据」
// 的约定一致）；接入对象存储时只替换落盘与出图两处。
// ============================================================================

// handleUploadAppeal POST /api/v1/appeals/upload
//
// multipart 字段：teamId(必填) / roundNo(可选 1|2) / disputeId(可选) /
//
//	operator(可选) / file(必填，图片)。
//
// 流程：写盘（服务端目录）→ 登记 evidence(kind=appeal, source=referee_submit,
// dispute_id=该争议单) → 返回证据元数据。
func (s *Server) handleUploadAppeal(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		Fail(w, r, NewBadRequest("请求体须为 multipart/form-data，且单文件 ≤ 10MB"))
		return
	}
	teamID, err := strconv.ParseInt(r.FormValue("teamId"), 10, 64)
	if err != nil || teamID <= 0 {
		Fail(w, r, NewBadRequest("teamId 必须为正整数"))
		return
	}
	operator := strings.TrimSpace(r.FormValue("operator"))

	var roundNo *int
	if raw := strings.TrimSpace(r.FormValue("roundNo")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || (v != 1 && v != 2) {
			Fail(w, r, NewBadRequest("roundNo 只能是 1 或 2"))
			return
		}
		roundNo = &v
	}
	var disputeID int64
	if raw := strings.TrimSpace(r.FormValue("disputeId")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			Fail(w, r, NewBadRequest("disputeId 必须为正整数"))
			return
		}
		disputeID = v
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		Fail(w, r, NewBadRequest("缺少文件字段 file"))
		return
	}
	defer file.Close()

	// 落盘：服务端目录按 SCORE_UPLOAD_DIR 配置（默认 uploads/appeals）。
	if err := os.MkdirAll(s.cfg.UploadDir, 0o755); err != nil {
		Fail(w, r, err)
		return
	}
	ext := filepath.Ext(header.Filename)
	rel := fmt.Sprintf("appeal_%d_%d%s", disputeID, time.Now().UnixNano(), ext)
	full := filepath.Join(s.cfg.UploadDir, rel)
	out, err := os.Create(full)
	if err != nil {
		Fail(w, r, err)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		Fail(w, r, err)
		return
	}
	out.Close()

	var dispID *int64
	if disputeID > 0 {
		dispID = &disputeID
	}
	e := &model.Evidence{
		TeamID:     teamID,
		RoundNo:    roundNo,
		Kind:       model.EvAppeal,
		Source:     model.SrcRefereeAppeal, // 裁判拍照上传申述书
		FileName:   strings.TrimSpace(header.Filename),
		Status:     model.EvLocal, // 一律先落本地，联网后补传再置 synced
		Operator:   operator,
		DisputeID:  dispID,
		StorageURL: rel,
	}
	created, err := s.svc.RecordEvidence(r.Context(), e)
	if err != nil {
		// 文件已落盘但登记失败：保留文件，交由后续补登记 / 人工处理
		Fail(w, r, err)
		return
	}
	Created(w, created)
}

// handleListAppeals GET /api/v1/appeals?disputeId=12
//
// 取某争议工单关联的申述书照片（P8 裁定台展示原件用）。
func (s *Server) handleListAppeals(w http.ResponseWriter, r *http.Request) {
	disputeID, err := queryInt64(r, "disputeId")
	if err != nil {
		Fail(w, r, err)
		return
	}
	list, err := s.svc.AppealForDispute(r.Context(), disputeID)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"evidence": list, "total": len(list)})
}

// handleGetAppealFile GET /api/v1/appeals/{id}/file
//
// 出图：按 evidence.storage_url 从服务端目录读文件流。
func (s *Server) handleGetAppealFile(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	e, err := s.svc.AppealByID(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	full := filepath.Join(s.cfg.UploadDir, e.StorageURL)
	data, err := os.ReadFile(full)
	if err != nil {
		Fail(w, r, NewNotFound("申述书照片文件不存在"))
		return
	}
	ctype := mime.TypeByExtension(filepath.Ext(e.StorageURL))
	if ctype == "" {
		ctype = "image/jpeg"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
