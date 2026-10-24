package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// StringList 是以 JSON 数组落库（Postgres jsonb）的字符串集合。
// 落库形态固定为 JSON 数组而非逗号分隔文本，避免取值本身含分隔符时被拆错；
// Scan 同时兼容历史单值文本，便于迁移前后的数据读取。
type StringList []string

// Value 实现 driver.Valuer：空集合落 "[]" 而不是 NULL，避免调用方处理 NULL。
func (l StringList) Value() (driver.Value, error) {
	if len(l) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal([]string(l))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner：接受 JSON 数组或历史单值字符串。
func (l *StringList) Scan(src any) error {
	if src == nil {
		*l = StringList{}
		return nil
	}
	var raw string
	switch v := src.(type) {
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		return fmt.Errorf("StringList: 不支持的扫描类型 %T", src)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		*l = StringList{}
		return nil
	}
	if !strings.HasPrefix(raw, "[") {
		*l = StringList{raw}
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return fmt.Errorf("StringList: 解析 %q 失败: %w", raw, err)
	}
	*l = out
	return nil
}

// MarshalJSON 保证 nil 集合序列化为 []（而非 null），前端无需处理 null。
func (l StringList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(l))
}

// UnmarshalJSON 接受 JSON 数组或单个字符串（兼容旧版单值入参）。
func (l *StringList) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		*l = StringList{}
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var out []string
		if err := json.Unmarshal(b, &out); err != nil {
			return err
		}
		*l = out
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*l = StringList{one}
	return nil
}
