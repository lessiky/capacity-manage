package app

import (
	"net/http"
	"testing"
	"time"

	"capacity-manage/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesIncludesDeleteOpenAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	registerRoutes(
		nil, v1, nil, "test-secret", time.Minute, &middleware.AuthZ{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)

	for _, route := range engine.Routes() {
		if route.Method == http.MethodDelete && route.Path == "/api/v1/open-api/keys/:id" {
			return
		}
	}
	t.Fatal("DELETE /api/v1/open-api/keys/:id is not registered")
}

func TestRegisterRoutesIncludesAlertDashboard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	registerRoutes(
		nil, v1, nil, "test-secret", time.Minute, &middleware.AuthZ{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)

	want := map[string]bool{
		http.MethodGet + " /api/v1/alerts/dashboard":            false,
		http.MethodGet + " /api/v1/alerts/dashboard-preference": false,
		http.MethodPut + " /api/v1/alerts/dashboard-preference": false,
		http.MethodGet + " /api/v1/alerts/dashboard-options":    false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Fatalf("%s is not registered", route)
		}
	}
}
