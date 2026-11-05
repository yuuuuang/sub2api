//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardAsAnthropic_TerraOAuthCacheSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{
		ID: 101, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
	}
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	forward := func(apiKeyID int64, explicitKey, laterTurn string) string {
		t.Helper()
		body := []byte(`{"model":"gpt-6.1-terra","max_tokens":32,"stream":false,"system":[{"type":"text","text":"fixed instructions","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}` + laterTurn + `]}`)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
		c.Set("api_key", &APIKey{ID: apiKeyID})
		upstream.resp = &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_terra","model":"gpt-6.1-terra","status":"completed","output":[],"usage":{"input_tokens":16,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}}` + "\n\n")),
		}
		_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, explicitKey, "")
		require.NoError(t, err)
		session := upstream.lastReq.Header.Get("session_id")
		require.NotEmpty(t, session, "Terra OAuth 转发必须携带上游缓存会话身份")
		require.Contains(t, string(upstream.lastBody), `"model":"gpt-6.1-terra"`)
		return session
	}

	first := forward(99, "", "")
	second := forward(99, "", `,{"role":"assistant","content":"hi"},{"role":"user","content":"continue"}`)
	require.Equal(t, first, second, "后续轮次应复用缓存会话身份")
	require.NotEqual(t, first, forward(100, "", ""), "不同 API Key 的缓存会话必须隔离")
	explicitKey := "client-cache-key"
	expected := generateSessionUUID(isolateOpenAIUpstreamSessionID(99, codexAccountIdentitySource(nil, account), explicitKey))
	require.Equal(t, expected, forward(99, explicitKey, ""), "保留客户端显式缓存身份")
}
