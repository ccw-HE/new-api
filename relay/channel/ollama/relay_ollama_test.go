package ollama

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestPullOllamaModelRejectsPrivateUpstreamURL(t *testing.T) {
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

	err := PullOllamaModel("http://127.0.0.1", "", "test-model")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private IP")
}
