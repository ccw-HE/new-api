package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestDoWorkerRequestRejectsPrivateWorkerURL(t *testing.T) {
	originalWorkerURL := system_setting.WorkerUrl
	originalFetchSetting := *system_setting.GetFetchSetting()
	t.Cleanup(func() {
		system_setting.WorkerUrl = originalWorkerURL
		*system_setting.GetFetchSetting() = originalFetchSetting
	})

	system_setting.WorkerUrl = "http://127.0.0.1:1"
	*system_setting.GetFetchSetting() = system_setting.FetchSetting{
		EnableSSRFProtection:   true,
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		AllowedPorts:           []string{"80", "443"},
		ApplyIPFilterForDomain: false,
	}
	InitHttpClient()

	_, err := DoWorkerRequest(&WorkerRequest{URL: "https://example.com/image.png"})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "worker URL") || strings.Contains(err.Error(), "private IP"), err.Error())
}
