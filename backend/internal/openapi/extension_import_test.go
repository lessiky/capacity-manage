package openapi

import (
	"strings"
	"testing"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/testutil"
)

func TestParseMetricCSVIncludesCategory(t *testing.T) {
	rows, err := parseMetricCSV(strings.NewReader(
		"metricCode,name,systemCode,category,unit,judgeDirection,warnLine,dangerLine\n" +
			"CPU,CPU使用率,S1,计算,%,higher_worse,80,90\n",
	))
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 1 || rows[0].Category != "计算" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUpsertImportedMetricUsesPredefinedCategory(t *testing.T) {
	h := testutil.NewDBHarness(t)
	h.Migrate(&persistence.BusinessSystem{}, &persistence.MetricCategory{}, &persistence.CapacityMetric{})
	if err := h.DB.Create(&persistence.BusinessSystem{
		SystemCode: "S1", Name: "系统一", Owner: "admin", Status: "normal",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&persistence.MetricCategory{
		SystemCode: "S1", Name: "计算",
	}).Error; err != nil {
		t.Fatal(err)
	}
	handler := &Handler{db: h.DB}

	if reason := handler.upsertImportedMetric("CPU", "CPU使用率", "S1", "", "%", "higher_worse", nil, nil); reason != "" {
		t.Fatalf("default category import failed: %s", reason)
	}
	if reason := handler.upsertImportedMetric("MEM", "内存使用率", "S1", "计算", "%", "higher_worse", nil, nil); reason != "" {
		t.Fatalf("predefined category import failed: %s", reason)
	}
	if reason := handler.upsertImportedMetric("DISK", "磁盘使用率", "S1", "不存在", "%", "higher_worse", nil, nil); reason == "" {
		t.Fatal("unknown category should fail")
	}

	var cpu persistence.CapacityMetric
	if err := h.DB.Where("metric_code = ?", "CPU").First(&cpu).Error; err != nil {
		t.Fatal(err)
	}
	if cpu.Category != "默认分类" {
		t.Fatalf("default category = %q", cpu.Category)
	}
	var memory persistence.CapacityMetric
	if err := h.DB.Where("metric_code = ?", "MEM").First(&memory).Error; err != nil {
		t.Fatal(err)
	}
	if memory.Category != "计算" {
		t.Fatalf("category = %q", memory.Category)
	}
	var n int64
	if err := h.DB.Model(&persistence.MetricCategory{}).
		Where("system_code = ? AND name = ?", "S1", "默认分类").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("default category rows = %d", n)
	}
}
