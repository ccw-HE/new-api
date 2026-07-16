package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestGetResponseBodyRejectsPrivateUpstreamURL(t *testing.T) {
	originalFetchSetting := *system_setting.GetFetchSetting()
	t.Cleanup(func() {
		*system_setting.GetFetchSetting() = originalFetchSetting
	})
	*system_setting.GetFetchSetting() = system_setting.FetchSetting{
		EnableSSRFProtection:   true,
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		AllowedPorts:           []string{"80", "443"},
		ApplyIPFilterForDomain: false,
	}

	_, err := GetResponseBody(http.MethodGet, "http://127.0.0.1/", &model.Channel{}, http.Header{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "private IP")
}
