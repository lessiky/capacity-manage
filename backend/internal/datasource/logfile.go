package datasource

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// LogFile 单个匹配的日志文件（路径 + 原始字节）
type LogFile struct {
	Path string
	Data []byte
}

// maxLogFiles 单次采集最多读取的匹配文件数（防目录异常膨胀）
const maxLogFiles = 256

// ReadLogFiles 读取匹配 pattern 的全部文件（sftp/local 双支持），
// 与 ReadLogFile 不同: 返回全部匹配（上限 maxLogFiles, 单文件 64MB）
func ReadLogFiles(ctx context.Context, cfg *Config, pattern string) ([]LogFile, error) {
	if pattern == "" {
		pattern = cfg.GlobTemplate
	}
	if pattern == "" {
		pattern = cfg.PathTemplate
	}
	if pattern == "" {
		return nil, fmt.Errorf("文件匹配模式为空")
	}
	if cfg.Scheme == "sftp" {
		client, err := dialSFTP(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		matches, err := client.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("glob 失败: %w", err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("无匹配文件: %s", pattern)
		}
		if len(matches) > maxLogFiles {
			matches = matches[:maxLogFiles]
		}
		out := make([]LogFile, 0, len(matches))
		for _, m := range matches {
			f, err := client.Open(m)
			if err != nil {
				return nil, fmt.Errorf("打开远程文件失败: %w", err)
			}
			data, err := io.ReadAll(io.LimitReader(f, 64<<20))
			_ = f.Close()
			if err != nil {
				return nil, err
			}
			out = append(out, LogFile{Path: m, Data: data})
		}
		return out, nil
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob 无效: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("无匹配文件: %s", pattern)
	}
	if len(matches) > maxLogFiles {
		matches = matches[:maxLogFiles]
	}
	out := make([]LogFile, 0, len(matches))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		out = append(out, LogFile{Path: m, Data: data})
	}
	return out, nil
}

// DecodeLog 按数据源 encoding 配置解码日志字节:
// 空/utf-8 直接转 string; gbk/gb2312 用 GBK 解码; gb18030 用 GB18030 解码
func DecodeLog(data []byte, encoding string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "utf8":
		return string(data), nil
	case "gbk", "gb2312":
		decoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), data)
		if err != nil {
			return "", fmt.Errorf("GBK 解码失败: %w", err)
		}
		return string(decoded), nil
	case "gb18030":
		decoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), data)
		if err != nil {
			return "", fmt.Errorf("GB18030 解码失败: %w", err)
		}
		return string(decoded), nil
	default:
		return "", fmt.Errorf("不支持的日志编码: %s (可选 utf-8/gbk/gb18030)", encoding)
	}
}
