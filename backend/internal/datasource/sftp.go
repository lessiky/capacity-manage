package datasource

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	gssh "golang.org/x/crypto/ssh"
	"github.com/pkg/sftp"
)

// dialSSH 建立 SSH 客户端（MVP: 密码认证 + HostKey 跳过校验；
// 生产建议接入 known_hosts 或证书认证，见总体架构安全设计）。
// 供日志 SFTP 读取与远程脚本执行(exec)共用。
func dialSSH(ctx context.Context, cfg *Config) (*gssh.Client, error) {
	sshConf := &gssh.ClientConfig{
		User:            cfg.Username,
		Auth:            []gssh.AuthMethod{gssh.Password(cfg.Password)},
		HostKeyCallback: gssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	netConn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	sshConn, chans, reqs, err := gssh.NewClientConn(netConn, addr, sshConf)
	if err != nil {
		_ = netConn.Close()
		return nil, fmt.Errorf("SSH 认证失败: %w", err)
	}
	return gssh.NewClient(sshConn, chans, reqs), nil
}

// dialSFTP 建立 SFTP 会话
func dialSFTP(ctx context.Context, cfg *Config) (*sftp.Client, error) {
	sshClient, err := dialSSH(ctx, cfg)
	if err != nil {
		return nil, err
	}
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, fmt.Errorf("SFTP 会话初始化失败: %w", err)
	}
	return client, nil
}

// ReadLogFile 读取日志源首个匹配文件全部内容（MVP: 单文件, 上限 64MB）
func ReadLogFile(ctx context.Context, cfg *Config, pattern string) ([]byte, []string, error) {
	if pattern == "" {
		pattern = cfg.PathTemplate
	}
	if cfg.Scheme == "sftp" {
		client, err := dialSFTP(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		defer client.Close()
		matches, err := client.Glob(pattern)
		if err != nil {
			return nil, nil, fmt.Errorf("glob 失败: %w", err)
		}
		if len(matches) == 0 {
			return nil, nil, fmt.Errorf("无匹配文件: %s", pattern)
		}
		f, err := client.Open(matches[0])
		if err != nil {
			return nil, nil, fmt.Errorf("打开远程文件失败: %w", err)
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 64<<20))
		if err != nil {
			return nil, nil, err
		}
		return data, matches, nil
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("glob 无效: %w", err)
	}
	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("无匹配文件: %s", pattern)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, nil, err
	}
	return data, matches, nil
}

// ListLogDir 列目录（连接测试/健康巡检用）
func ListLogDir(ctx context.Context, cfg *Config, dir string) (int, error) {
	if cfg.Scheme == "sftp" {
		client, err := dialSFTP(ctx, cfg)
		if err != nil {
			return 0, err
		}
		defer client.Close()
		if dir == "" {
			dir = "/"
		}
		entries, err := client.ReadDir(dir)
		if err != nil {
			return 0, err
		}
		return len(entries), nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}
