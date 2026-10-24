package flow

import (
	"testing"
)

func mustF(v float64) *float64 { return &v }

func TestCleanPhase1TypeAndUnit(t *testing.T) {
	rows := CleanPhase1([]RawRow{
		{Raw: "512", Unit: "MB"},
		{Raw: "abc"},
		{Raw: " 10.5 "},
	}, CleanConfig{}, "GB")
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Value == nil || *rows[0].Value != 0.5 {
		t.Fatalf("MB->GB = %v", rows[0].Value)
	}
	if !rows[1].IsAnomaly || rows[1].Value != nil {
		t.Fatalf("non-numeric should be anomaly: %+v", rows[1])
	}
	if rows[2].Value == nil || *rows[2].Value != 10.5 {
		t.Fatalf("trim parse = %v", rows[2].Value)
	}
}

func TestCleanPhase1Transforms(t *testing.T) {
	rows := CleanPhase1([]RawRow{{Raw: "100"}}, CleanConfig{
		Transforms: []Transform{{Type: "linear", A: 2, B: 1}, {Type: "round", Digits: 0}},
	}, "")
	// 100*2+1 = 201
	if rows[0].Value == nil || *rows[0].Value != 201 {
		t.Fatalf("linear+round = %v", rows[0].Value)
	}
}

func TestCleanPhase2UsageRange(t *testing.T) {
	rows := CleanPhase1([]RawRow{{Raw: "120"}, {Raw: "50"}}, CleanConfig{}, "")
	_, final := CleanPhase2(rows, CleanConfig{}, "usage", "latest")
	// 120 超域, 50 正常, latest 取值 => 取有效末位 50
	if final.IsAnomaly || final.Value == nil || *final.Value != 50 {
		t.Fatalf("usage range final = %+v", final)
	}
}

func TestCleanPhase2Negative(t *testing.T) {
	rows := CleanPhase1([]RawRow{{Raw: "-3"}}, CleanConfig{}, "")
	_, final := CleanPhase2(rows, CleanConfig{}, "absolute", "latest")
	if !final.IsAnomaly {
		t.Fatalf("negative should be anomaly: %+v", final)
	}
}

func TestCleanDedupModes(t *testing.T) {
	base := func() []CleanRow {
		return CleanPhase1([]RawRow{{Raw: "10"}, {Raw: "20"}, {Raw: "30"}}, CleanConfig{}, "")
	}
	cases := map[string]float64{
		"latest": 30, "first": 10, "avg": 20, "max": 30, "min": 10, "sum": 60,
	}
	for mode, want := range cases {
		_, final := CleanPhase2(base(), CleanConfig{DedupTake: mode}, "absolute", "latest")
		if final.Value == nil || *final.Value != want {
			t.Fatalf("mode %s = %v, want %v", mode, final.Value, want)
		}
	}
	// 默认按指标 stat_mode
	_, final := CleanPhase2(base(), CleanConfig{}, "absolute", "avg")
	if final.Value == nil || *final.Value != 20 {
		t.Fatalf("statMode default = %v", final.Value)
	}
}

func TestCleanAllAnomaly(t *testing.T) {
	rows := CleanPhase1([]RawRow{{Raw: "xx"}, {Raw: "yy"}}, CleanConfig{}, "")
	_, final := CleanPhase2(rows, CleanConfig{}, "absolute", "latest")
	if !final.IsAnomaly || final.Value != nil {
		t.Fatalf("all anomaly final = %+v", final)
	}
}

func TestCleanRawValuePtr(t *testing.T) {
	cr := CleanRow{Raw: "12.5", Value: mustF(12.5)}
	if cr.RawValuePtr() == nil || *cr.RawValuePtr() != 12.5 {
		t.Fatalf("RawValuePtr = %v", cr.RawValuePtr())
	}
	cr2 := CleanRow{Raw: "abc"}
	if cr2.RawValuePtr() != nil {
		t.Fatalf("RawValuePtr non-numeric should be nil")
	}
}
