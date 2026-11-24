package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMountMediaDispatchesByKeyPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	answer := func(who string) gin.HandlerFunc {
		return func(c *gin.Context) { c.String(http.StatusOK, who+":"+c.Param("key")) }
	}
	mountMedia(engine.Group("/v1"), answer("property"), answer("announcements"))
	for path, want := range map[string]string{
		"/v1/media/announcements/2026/11/a.webp": "announcements:/announcements/2026/11/a.webp",
		"/v1/media/property/2026/11/a.webp":      "property:/property/2026/11/a.webp",
		"/v1/media/other/x":                      "property:/other/x",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if method == http.MethodGet && w.Body.String() != want {
				t.Errorf("%s: want %q, got %q", path, want, w.Body.String())
			}
			if w.Code != http.StatusOK {
				t.Errorf("%s %s: %d", method, path, w.Code)
			}
		}
	}
}
