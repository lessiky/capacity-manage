package data

import (
	"testing"
	"time"
)

// 回归: 手工汇总的 bizDate 必须按业务本地时区解析, 不能解析成 UTC 午夜。
func TestParseSummaryBizDateUsesLocalLocation(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	got, err := parseBizDateInLocation("2026-10-26", loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 26, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("bizDate = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if got.Location() != loc {
		t.Fatalf("location = %s, want %s", got.Location(), loc)
	}
}
