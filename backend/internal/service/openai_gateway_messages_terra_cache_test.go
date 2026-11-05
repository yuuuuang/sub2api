//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardAsAnthropic_TerraOAuthCacheSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{
		ID: 101, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	forward := func(model string, apiKeyID int64, explicitKey, metadata, laterTurn string) string {
		t.Helper()
		body := []byte(`{"model":"` + model + `","max_tokens":32,"stream":false,"metadata":` + metadata + `,"system":[{"type":"text","text":"fixed instructions","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}` + laterTurn + `]}`)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
		c.Set("api_key", &APIKey{ID: apiKeyID})
		upstream.resp = openAICompatSSECompletedResponse("resp_terra", model)
		_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, explicitKey, "")
		require.NoError(t, err)
		require.NotContains(t, string(upstream.lastBody), openAICompatClaudeCodeTodoGuardMarker)
		require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
		require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
		var input []json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(upstream.lastBody, "input").Raw), &input))
		require.GreaterOrEqual(t, len(input), int(gjson.GetBytes(body, "messages.#").Int()), "必须保留完整历史")
		return upstream.lastReq.Header.Get("session_id")
	}

	first := forward("gpt-6.1-terra", 99, "", `{}`, "")
	require.NotEmpty(t, first, "Terra OAuth 转发必须携带上游缓存会话身份")
	later := `,{"role":"assistant","content":"hi"},{"role":"user","content":"continue"}`
	require.Equal(t, first, forward("gpt-6.1-terra", 99, "", `{}`, later))
	require.NotEqual(t, first, forward("gpt-6.1-terra", 100, "", `{}`, ""), "不同 API Key 必须隔离")
	expected := generateSessionUUID(isolateOpenAIUpstreamSessionID(99, account, "client-cache-key"))
	require.Equal(t, expected, forward("gpt-6.1-terra", 99, "client-cache-key", `{}`, ""))

	metadata := `{"user_id":"{\"device_id\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"account_uuid\":\"\",\"session_id\":\"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\"}"}`
	metadataSession := forward("gpt-6.1-terra", 99, "", metadata, "")
	require.NotEmpty(t, metadataSession)
	require.NotEqual(t, first, metadataSession, "metadata 会话身份应优先于缓存锚点")
	require.Equal(t, metadataSession, forward("gpt-6.1-terra", 99, "", metadata, later))
	for i := 0; i < 8; i++ {
		later += `,{"role":"assistant","content":"hi"},{"role":"user","content":"continue"}`
	}
	require.Equal(t, metadataSession, forward("gpt-6.1-terra", 99, "", metadata, later))

	for _, model := range []string{"gpt-6.1-terra-preview", "gpt-6.1-terrain"} {
		require.Empty(t, forward(model, 99, "", `{}`, ""), "不自动适配未知型号")
	}
	account.Type = AccountTypeAPIKey
	account.Credentials = map[string]any{"api_key": "test-key", "base_url": "http://upstream.example"}
	account.Extra = map[string]any{"openai_responses_supported": true}
	require.Empty(t, forward("gpt-6.1-terra", 99, "", `{}`, later), "API Key 路径保持原样")
}
