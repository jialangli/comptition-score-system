package api_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/api"
)

// ============================================================================
// 申述书照片 HTTP 层测试 —— 走完整中间件链 + 真实 PG
//
// 覆盖：POST /api/v1/appeals/upload（落盘+登记）→
//       GET  /api/v1/appeals?disputeId=（按争议单取图）→
//       GET  /api/v1/appeals/{id}/file（出图）。
// 连不上测试库时跳过（见 api_test.go 的 newTestServer）。
// ============================================================================

// uploadAppeal 以 multipart 形式上传申述书，返回解包后的响应。
//
// filename 传空串表示「故意不带 file 字段」，用于测缺文件校验。
func (ts *testServer) uploadAppeal(t *testing.T, fields map[string]string, content []byte, filename string) resp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("写表单字段失败: %v", err)
		}
	}
	if filename != "" {
		fw, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("建文件字段失败: %v", err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("写文件内容失败: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("结束 multipart 失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/appeals/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(api.OperatorHeader, "裁判A")
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	return decodeAppealResp(t, rec)
}

func decodeAppealResp(t *testing.T, rec *httptest.ResponseRecorder) resp {
	t.Helper()
	out := resp{Status: rec.Code}
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON：%s\n%s", rec.Body.String(), err)
	}
	out.Code, out.Msg, out.Data = env.Code, env.Message, env.Data
	return out
}

// TestAppealUploadHappyPath 上传 → 登记 → 列表 → 出图 全链路。
func TestAppealUploadHappyPath(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "1001", "申述队", "小学组")

	// 一条争议工单，申述书挂在它下
	var disp struct {
		ID int64 `json:"id"`
	}
	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId":  team.ID,
		"roundNo": 1,
		"kind":    "other",
		"reason":  "对第 1 轮判罚有异议，提交手写申述书",
	}, "裁判A").expect(t, http.StatusCreated).as(t, &disp)

	img := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46} // 伪造 JPEG 头

	up := ts.uploadAppeal(t, map[string]string{
		"teamId":    itoa(team.ID),
		"roundNo":   "1",
		"disputeId": itoa(disp.ID),
		"operator":  "裁判A",
	}, img, "appeal_T1001_R1.jpg").expect(t, http.StatusCreated)

	var evd struct {
		ID          int64  `json:"id"`
		Kind        string `json:"kind"`
		KindLabel   string `json:"kindLabel"`
		Source      string `json:"source"`
		SourceLabel string `json:"sourceLabel"`
		DisputeID   *int64 `json:"disputeId"`
		StorageURL  string `json:"storageUrl"`
		Status      string `json:"status"`
	}
	up.as(t, &evd)
	if evd.ID == 0 {
		t.Fatal("上传后证据 ID 应被回填")
	}
	if evd.Kind != "appeal" || evd.KindLabel != "申述书照片" {
		t.Fatalf("kind 应为 appeal/申述书照片，实际 %q/%q", evd.Kind, evd.KindLabel)
	}
	if evd.Source != "referee_appeal" || evd.SourceLabel != "裁判拍照上传(申述书)" {
		t.Fatalf("source 应为 referee_appeal，实际 %q/%q", evd.Source, evd.SourceLabel)
	}
	if evd.DisputeID == nil || *evd.DisputeID != disp.ID {
		t.Fatalf("应关联争议单 %d，实际 %v", disp.ID, evd.DisputeID)
	}
	if evd.Status != "local" {
		t.Errorf("新登记应处于本地待同步，实际 %q", evd.Status)
	}

	// 列表：按争议单取回
	var list struct {
		Evidence []struct {
			ID int64 `json:"id"`
		} `json:"evidence"`
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/appeals?disputeId="+itoa(disp.ID), nil, "").
		expect(t, http.StatusOK).as(t, &list)
	if list.Total != 1 || len(list.Evidence) != 1 || list.Evidence[0].ID != evd.ID {
		t.Fatalf("列表应含 1 张申述书且 ID 匹配：%+v", list)
	}

	// 出图：读回原文件字节，且 MIME 正确
	req := httptest.NewRequest(http.MethodGet, "/api/v1/appeals/"+itoa(evd.ID)+"/file", nil)
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("出图应 200，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("JPEG 应返回 image/jpeg，实际 %q", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), img) {
		t.Errorf("出图内容应与上传字节完全一致（期望 %d 字节，实际 %d 字节）", len(img), rec.Body.Len())
	}
}

// TestAppealUploadValidation 缺文件 / teamId 非法 → 400。
func TestAppealUploadValidation(t *testing.T) {
	ts := newTestServer(t)

	// 缺 file 字段：multipart 没有 file 部分
	noFile := ts.uploadAppeal(t, map[string]string{"teamId": "1001"}, nil, "")
	noFile.expect(t, http.StatusBadRequest)

	// teamId 非法
	badTeam := ts.uploadAppeal(t, map[string]string{"teamId": "abc"}, []byte("x"), "a.jpg")
	badTeam.expect(t, http.StatusBadRequest)

	// disputeId 非法
	badDisp := ts.uploadAppeal(t, map[string]string{
		"teamId": "1001", "disputeId": "zzz",
	}, []byte("x"), "a.jpg")
	badDisp.expect(t, http.StatusBadRequest)
}

// TestAppealFileNotFound 出图时证据在库但物理文件丢失 → 404。
func TestAppealFileNotFound(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "1001", "申述队", "小学组")

	var disp struct {
		ID int64 `json:"id"`
	}
	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId":  team.ID,
		"roundNo": 1,
		"kind":    "other",
		"reason":  "对第 1 轮判罚有异议，提交手写申述书",
	}, "裁判A").expect(t, http.StatusCreated).as(t, &disp)

	img := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}
	up := ts.uploadAppeal(t, map[string]string{
		"teamId":    itoa(team.ID),
		"roundNo":   "1",
		"disputeId": itoa(disp.ID),
		"operator":  "裁判A",
	}, img, "appeal_T1001_R1.jpg").expect(t, http.StatusCreated)

	var evd struct {
		ID         int64  `json:"id"`
		StorageURL string `json:"storageUrl"`
	}
	up.as(t, &evd)

	// 模拟「证据元数据在、但落盘文件被清理 / 对象存储未同步」：删掉物理文件
	if err := os.Remove(filepath.Join(ts.uploadDir, evd.StorageURL)); err != nil {
		t.Fatalf("删除上传文件失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/appeals/"+itoa(evd.ID)+"/file", nil)
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("文件缺失应 404，实际 %d：%s", rec.Code, rec.Body.String())
	}
}
