package router

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelSchedulerRoutesIncludeHyphenCompatibleAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	require.Contains(t, routes, "GET /api/channel_scheduler/logs")
	require.Contains(t, routes, "GET /api/channel_scheduler/logs/stat")
	require.Contains(t, routes, "DELETE /api/channel_scheduler/logs")
	require.Contains(t, routes, "GET /api/channel_scheduler/disabled")
	require.Contains(t, routes, "GET /api/channel_scheduler/config")
	require.Contains(t, routes, "PUT /api/channel_scheduler/config")
	require.Contains(t, routes, "GET /api/channel_scheduler/channel/:id/config")
	require.Contains(t, routes, "PUT /api/channel_scheduler/channel/:id/config")
	require.Contains(t, routes, "POST /api/channel_scheduler/restore/:id")

	assert.Contains(t, routes, "GET /api/channel-scheduler/logs")
	assert.Contains(t, routes, "GET /api/channel-scheduler/logs/stat")
	assert.Contains(t, routes, "DELETE /api/channel-scheduler/logs")
	assert.Contains(t, routes, "GET /api/channel-scheduler/disabled")
	assert.Contains(t, routes, "GET /api/channel-scheduler/config")
	assert.Contains(t, routes, "PUT /api/channel-scheduler/config")
	assert.Contains(t, routes, "GET /api/channel-scheduler/channel/:id/config")
	assert.Contains(t, routes, "PUT /api/channel-scheduler/channel/:id/config")
	assert.Contains(t, routes, "POST /api/channel-scheduler/restore/:id")
}
