package system

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func newBrandingTestHandler(t *testing.T) (*Handler, *gorm.DB, *Params) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:branding-"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"),
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&persistence.SysParam{}); err != nil {
		t.Fatalf("migrate sys_param: %v", err)
	}
	params := NewParams(db)
	return NewHandler(db, params, nil, nil), db, params
}

func seedParam(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()
	if err := db.Create(&persistence.SysParam{Key: key, Value: value, Category: "general"}).Error; err != nil {
		t.Fatalf("seed %s: %v", key, err)
	}
}

func brandingRequest(t *testing.T, h *Handler, method string, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	switch method {
	case http.MethodGet:
		r.GET("/branding", h.Branding)
	case http.MethodPost:
		r.POST("/params/platform-icon", h.UploadPlatformIcon)
	case http.MethodDelete:
		r.DELETE("/params/platform-icon", h.ClearPlatformIcon)
	default:
		t.Fatalf("unsupported method %s", method)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeBranding(t *testing.T, w *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var body struct {
		Code int `json:"code"`
		Data struct {
			Name string `json:"name"`
			Icon string `json:"icon"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	if body.Code != 0 {
		t.Fatalf("response code = %d, want 0: %s", body.Code, w.Body.String())
	}
	return body.Data.Name, body.Data.Icon
}

func TestBrandingFallsBackToDefaultPlatformName(t *testing.T) {
	h, _, _ := newBrandingTestHandler(t)
	w := brandingRequest(t, h, http.MethodGet, httptest.NewRequest(http.MethodGet, "/branding", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	name, icon := decodeBranding(t, w)
	if name != "容量管理平台" {
		t.Fatalf("name = %q, want 容量管理平台", name)
	}
	if icon != "" {
		t.Fatalf("icon = %q, want empty", icon)
	}
}

func TestBrandingReturnsConfiguredPlatformBrand(t *testing.T) {
	h, db, _ := newBrandingTestHandler(t)
	seedParam(t, db, "platform_name", "统一容量中枢")
	seedParam(t, db, "platform_icon", "data:image/png;base64,AAAA")

	w := brandingRequest(t, h, http.MethodGet, httptest.NewRequest(http.MethodGet, "/branding", nil))

	name, icon := decodeBranding(t, w)
	if name != "统一容量中枢" {
		t.Fatalf("name = %q, want 统一容量中枢", name)
	}
	if icon != "data:image/png;base64,AAAA" {
		t.Fatalf("icon = %q, want configured data URL", icon)
	}
}

func TestUploadPlatformIconStoresImageAsDataURL(t *testing.T) {
	h, db, params := newBrandingTestHandler(t)
	seedParam(t, db, "platform_icon", "")
	req := multipartImageRequest(t, "logo.png", append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...))

	w := brandingRequest(t, h, http.MethodPost, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var row persistence.SysParam
	if err := db.First(&row, "key = ?", "platform_icon").Error; err != nil {
		t.Fatalf("load platform_icon: %v", err)
	}
	if !strings.HasPrefix(row.Value, "data:image/png;base64,") {
		t.Fatalf("stored value = %q, want image/png data URL", row.Value)
	}
	if got := params.Get("platform_icon", ""); got != row.Value {
		t.Fatalf("cached icon = %q, want %q", got, row.Value)
	}
}

func TestUploadPlatformIconRejectsNonImage(t *testing.T) {
	h, db, _ := newBrandingTestHandler(t)
	seedParam(t, db, "platform_icon", "")
	req := multipartImageRequest(t, "logo.txt", []byte("not an image"))

	w := brandingRequest(t, h, http.MethodPost, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestUploadPlatformIconRejectsOversizedImage(t *testing.T) {
	h, db, _ := newBrandingTestHandler(t)
	seedParam(t, db, "platform_icon", "")
	data := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, maxPlatformIconBytes)...)
	req := multipartImageRequest(t, "logo.png", data)

	w := brandingRequest(t, h, http.MethodPost, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestClearPlatformIconEmptiesStoredValue(t *testing.T) {
	h, db, params := newBrandingTestHandler(t)
	seedParam(t, db, "platform_icon", "data:image/png;base64,AAAA")

	w := brandingRequest(t, h, http.MethodDelete, httptest.NewRequest(http.MethodDelete, "/params/platform-icon", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var row persistence.SysParam
	if err := db.First(&row, "key = ?", "platform_icon").Error; err != nil {
		t.Fatalf("load platform_icon: %v", err)
	}
	if row.Value != "" {
		t.Fatalf("stored value = %q, want empty", row.Value)
	}
	if got := params.Get("platform_icon", "missing"); got != "" {
		t.Fatalf("cached icon = %q, want empty", got)
	}
}

func multipartImageRequest(t *testing.T, filename string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/params/platform-icon", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}
