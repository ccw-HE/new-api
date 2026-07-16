package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildPasswordResetEmailEscapesLinkAndDisplayName(t *testing.T) {
	_, content := buildPasswordResetEmail(
		"New API\"><img src=x onerror=alert(1)>",
		"https://example.com/' onclick='alert(1)",
		"user+tag@example.com",
		"123456",
	)

	require.NotContains(t, content, "<img")
	require.NotContains(t, content, "href='https://example.com/' onclick='alert(1)")
	require.Contains(t, content, "&#39; onclick=&#39;")
	require.Contains(t, content, "email=user%2Btag%40example.com")
	require.True(t, strings.Contains(content, "&amp;token=123456") || strings.Contains(content, "%26token%3D123456"))
}
