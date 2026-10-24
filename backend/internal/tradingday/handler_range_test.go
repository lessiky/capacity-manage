package tradingday_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"capacity-manage/backend/internal/persistence"
	"capacity-manage/backend/internal/pkg/resp"
	"capacity-manage/backend/internal/testutil"
	"capacity-manage/backend/internal/tradingday"

	"github.com/gin-gonic/gin"
)

func TestListUsesExplicitDateRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	harness := testutil.NewDBHarness(t)
	harness.Migrate(&persistence.TradingDayCalendar{})

	rows := []persistence.TradingDayCalendar{
		{Market: "SH", CalDate: time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local), IsTrading: false, DayType: "holiday", Source: "manual"},
		{Market: "SH", CalDate: time.Date(2027, 12, 31, 0, 0, 0, 0, time.Local), IsTrading: true, DayType: "trading", Source: "manual"},
	}
	if err := harness.DB.Create(&rows).Error; err != nil {
		t.Fatalf("seed calendar rows: %v", err)
	}

	h := tradingday.NewHandler(nil, harness.DB, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/v1/trading-days?market=SH&from=2027-01-01&to=2027-12-31&page=1&pageSize=30", nil)
	h.List(c)

	var body resp.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, w.Body.String())
	}
	if body.Code != resp.CodeOK {
		t.Fatalf("list calendar failed: %+v", body)
	}
	raw, err := json.Marshal(body.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}
	var got struct {
		Items []struct {
			Date string `json:"date"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode page data: %v, data=%s", err, string(raw))
	}
	if got.Total != 2 || len(got.Items) != 2 {
		t.Fatalf("explicit 2027 range should return both rows: %+v", got)
	}
	if got.Items[0].Date != "2027-01-01" || got.Items[1].Date != "2027-12-31" {
		t.Fatalf("unexpected calendar rows: %+v", got.Items)
	}
}
