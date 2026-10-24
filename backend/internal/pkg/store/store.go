package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Store 文件存储抽象（一期本地磁盘，MinIO 适配实现同接口即可切换）
type Store interface {
	Save(key string, reader io.Reader) error
	Open(key string) (io.ReadCloser, error)
	Delete(key string) error
	Exists(key string) bool
	Size() (int64, error) // 全部文件总字节数（平台自检用）
}

type localStore struct{ root string }

func NewLocal(root string) (Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &localStore{root: root}, nil
}

func (s *localStore) path(key string) (string, error) {
	clean := filepath.Clean("/" + key)
	full := filepath.Join(s.root, filepath.FromSlash(clean))
	if !strings.HasPrefix(full, filepath.Clean(s.root)) {
		return "", fmt.Errorf("illegal storage key: %s", key)
	}
	return full, nil
}

func (s *localStore) Save(key string, reader io.Reader) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, reader)
	return err
}

func (s *localStore) Open(key string) (io.ReadCloser, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (s *localStore) Delete(key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func (s *localStore) Exists(key string) bool {
	p, err := s.path(key)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func (s *localStore) Size() (int64, error) {
	var total int64
	err := filepath.Walk(s.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}