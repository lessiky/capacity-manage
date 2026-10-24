package system

import (
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Params 系统参数缓存（DB + 本地缓存 30s + 写后主动失效，NFR 7.5 热生效）
type Params struct {
	db   *gorm.DB
	mu   sync.RWMutex
	m    map[string]string
	load time.Time
}

func NewParams(db *gorm.DB) *Params { return &Params{db: db} }

func (p *Params) refresh() {
	if time.Since(p.load) < 30*time.Second {
		return
	}
	var rows []struct {
		Key   string
		Value string
	}
	if err := p.db.Model(&struct{}{}).Table("sys_param").Select("key, value").Scan(&rows).Error; err != nil {
		return
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	p.mu.Lock()
	p.m = m
	p.load = time.Now()
	p.mu.Unlock()
}

func (p *Params) Get(key, def string) string {
	p.refresh()
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.m[key]; ok {
		return v
	}
	return def
}

func (p *Params) Int(key string, def int) int {
	if v, err := strconv.Atoi(p.Get(key, strconv.Itoa(def))); err == nil {
		return v
	}
	return def
}

func (p *Params) Float(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(p.Get(key, strconv.FormatFloat(def, 'f', -1, 64)), 64); err == nil {
		return v
	}
	return def
}

func (p *Params) Bool(key string, def bool) bool {
	if v, err := strconv.ParseBool(p.Get(key, strconv.FormatBool(def))); err == nil {
		return v
	}
	return def
}

// Invalidate 参数写后调用
func (p *Params) Invalidate() {
	p.mu.Lock()
	p.load = time.Time{}
	p.mu.Unlock()
}