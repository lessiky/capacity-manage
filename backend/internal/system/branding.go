package system

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"time"

	"capacity-manage/backend/internal/middleware"
	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"

	"github.com/gin-gonic/gin"
)

const (
	defaultPlatformName  = "容量管理平台"
	maxPlatformIconBytes = 1 << 20
)

var allowedPlatformIconTypes = map[string]bool{
	"image/png":                true,
	"image/jpeg":               true,
	"image/webp":               true,
	"image/gif":                true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
}

func (h *Handler) Branding(c *gin.Context) {
	resp.OK(c, gin.H{
		"name": h.params.Get("platform_name", defaultPlatformName),
		"icon": h.params.Get("platform_icon", ""),
	})
}

func (h *Handler) UploadPlatformIcon(c *gin.Context) {
	header, err := c.FormFile("file")
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "缺少图标文件: %v", err))
		return
	}
	if header.Size > maxPlatformIconBytes {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "图标不能超过 1 MiB"))
		return
	}
	f, err := header.Open()
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "图标打开失败: %v", err))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPlatformIconBytes+1))
	if err != nil {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "图标读取失败: %v", err))
		return
	}
	if len(data) == 0 {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "图标文件为空"))
		return
	}
	if len(data) > maxPlatformIconBytes {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "图标不能超过 1 MiB"))
		return
	}
	contentType := http.DetectContentType(data)
	if !allowedPlatformIconTypes[contentType] {
		resp.Fail(c, resp.NewErr(resp.CodeBadRequest, "仅支持 PNG/JPEG/WebP/GIF/ICO 图标"))
		return
	}
	value := "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data)
	h.putBrandingParam(c, "platform_icon", value, fmt.Sprintf("size=%d,mime=%s", len(data), contentType))
}

func (h *Handler) ClearPlatformIcon(c *gin.Context) {
	h.putBrandingParam(c, "platform_icon", "", "cleared")
}

func (h *Handler) putBrandingParam(c *gin.Context, key, value, detail string) {
	username, _ := c.Get(middleware.CtxUsername)
	un, _ := username.(string)
	result := h.db.Model(&persistence.SysParam{}).Where("key = ?", key).
		Updates(map[string]any{"value": value, "updated_by": un, "updated_at": time.Now()})
	if result.Error != nil {
		resp.Fail(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		resp.Fail(c, resp.NewErr(resp.CodeNotFound, "参数不存在: %s", key))
		return
	}
	h.params.Invalidate()
	middleware.Log(c, "system", "param.put", key, detail)
	resp.OK(c, nil)
}
