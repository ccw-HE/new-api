package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheMiddlewareDoesNotCacheApiResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(Cache())
	for _, path := range []string{"/api/missing", "/v1/models", "/mj/submit", "/pg/probe"} {
		engine.GET(path, func(c *gin.Context) {
			c.JSON(http.StatusNotFound, gin.H{"success": false})
		})
	}

	for _, path := range []string{"/api-assets/app.js", "/static/app.js"} {
		engine.GET(path, func(c *gin.Context) {
			c.String(http.StatusOK, "asset")
		})
	}

	for _, path := range []string{"/api/missing", "/v1/models", "/mj/submit", "/pg/probe"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		engine.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNotFound, recorder.Code)
		assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
		assert.NotContains(t, recorder.Header().Get("Cache-Control"), "max-age=604800")
	}

	for _, path := range []string{"/api-assets/app.js", "/static/app.js"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		engine.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "max-age=604800", recorder.Header().Get("Cache-Control"))
	}
}

func TestCacheMiddlewareKeepsRootUncached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(Cache())
	engine.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"success": false})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
}
