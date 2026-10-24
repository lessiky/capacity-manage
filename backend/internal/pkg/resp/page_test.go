package resp

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func pageContext(query string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/x"+query, nil)
	return c
}

func TestParsePageDisabledWithoutPageParam(t *testing.T) {
	for _, query := range []string{"", "?pageSize=50", "?page=0", "?page=-3", "?page=abc"} {
		if _, _, ok := ParsePage(pageContext(query), 20, 200); ok {
			t.Fatalf("query %q 没有有效 page 时应走兼容分支", query)
		}
	}
}

func TestParsePageReadsPageAndSize(t *testing.T) {
	page, size, ok := ParsePage(pageContext("?page=3&pageSize=50"), 20, 200)
	if !ok || page != 3 || size != 50 {
		t.Fatalf("page/pageSize 解析错误: page=%d size=%d ok=%v", page, size, ok)
	}
}

func TestParsePageFallsBackToDefaultSize(t *testing.T) {
	for _, query := range []string{"?page=2", "?page=2&pageSize=0", "?page=2&pageSize=-5", "?page=2&pageSize=abc"} {
		page, size, ok := ParsePage(pageContext(query), 20, 200)
		if !ok || page != 2 || size != 20 {
			t.Fatalf("query %q 应回退默认页大小: page=%d size=%d ok=%v", query, page, size, ok)
		}
	}
}

func TestParsePageClampsPageSizeToMax(t *testing.T) {
	_, size, ok := ParsePage(pageContext("?page=1&pageSize=5000"), 20, 200)
	if !ok || size != 200 {
		t.Fatalf("pageSize 应被限制到上限 200, 实际 %d", size)
	}
}
