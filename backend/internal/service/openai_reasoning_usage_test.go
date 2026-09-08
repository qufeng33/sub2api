package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const reasoningUsageResponse = `{"id":"resp_reasoning","object":"response","model":"gpt-5.1","status":"completed","output":[{"id":"msg_reasoning","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":10,"output_tokens":100,"total_tokens":110,"output_tokens_details":{"reasoning_tokens":50}}}`
const reasoningUsageChat = `{"id":"chatcmpl_reasoning","object":"chat.completion","model":"gpt-5.1","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":100,"total_tokens":110,"completion_tokens_details":{"reasoning_tokens":50}}}`

func TestOpenAIReasoningUsageHTTPPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"responses_json", "responses_sse", "responses_buffered_sse", "passthrough_json", "passthrough_sse", "passthrough_buffered_sse", "chat_stream", "chat_buffered", "raw_chat_json", "raw_chat_sse", "messages_stream", "messages_buffered"} {
		t.Run(name, func(t *testing.T) {
			coefficient := 1.5
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIReasoningBillingMultiplier: &coefficient}}, toolCorrector: NewCodexToolCorrector()}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			payload, contentType := "data: {\"type\":\"response.completed\",\"response\":"+reasoningUsageResponse+"}\n\n", "text/event-stream"
			if strings.HasSuffix(name, "json") {
				payload, contentType = reasoningUsageResponse, "application/json"
			}
			if name == "raw_chat_json" {
				payload = reasoningUsageChat
			}
			if name == "raw_chat_sse" {
				payload = "data: " + reasoningUsageChat + "\n\ndata: [DONE]\n\n"
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}
			var usage *OpenAIUsage
			var err error
			switch name {
			case "responses_json", "responses_buffered_sse":
				r, e := svc.handleNonStreamingResponse(c.Request.Context(), resp, c, account, "gpt-5.1", "gpt-5.1")
				err = e
				if r != nil {
					usage = r.OpenAIUsage
				}
			case "responses_sse":
				r, e := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
				err = e
				if r != nil {
					usage = r.usage
				}
			case "passthrough_json", "passthrough_buffered_sse":
				r, e := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, account, "gpt-5.1", "gpt-5.1")
				err = e
				if r != nil {
					usage = r.OpenAIUsage
				}
			case "passthrough_sse":
				r, e := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
				err = e
				if r != nil {
					usage = r.usage
				}
			default:
				var r *OpenAIForwardResult
				switch name {
				case "chat_stream":
					r, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now(), 0)
				case "chat_buffered":
					r, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
				case "raw_chat_json":
					r, err = svc.bufferRawChatCompletions(c, resp, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
				case "raw_chat_sse":
					r, err = svc.streamRawChatCompletions(c, resp, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now(), 0)
				case "messages_stream":
					r, err = svc.handleAnthropicStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
				case "messages_buffered":
					r, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
				}
				if r != nil {
					usage = &r.Usage
				}
			}
			require.NoError(t, err)
			require.NotNil(t, usage)
			require.Equal(t, 125, usage.OutputTokens)
			visible, ok := extractOpenAIUsageFromJSONBytes(rec.Body.Bytes())
			if !ok {
				visible = *svc.parseSSEUsageFromBody(rec.Body.String())
			}
			require.Equal(t, 125, visible.OutputTokens, "下游与计费使用相同的折算输出量")
		})
	}
}

func TestOpenAIReasoningUsageHTTPBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, stream := range []string{"true", "false"} {
			t.Run(accountType+"/stream="+stream, func(t *testing.T) {
				coefficient := 1.5
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + reasoningUsageResponse + "}\n\n")),
				}}
				svc := &OpenAIGatewayService{
					cfg:          &config.Config{Gateway: config.GatewayConfig{OpenAIReasoningBillingMultiplier: &coefficient}},
					httpUpstream: upstream,
				}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: accountType, Concurrency: 1}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
				request := []byte(`{"type":"response.create","model":"gpt-5.1","stream":` + stream + `,"input":"hi"}`)
				var terminal []byte
				result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "test-token", request, len(request),
					"gpt-5.1", "", "", "", "", 1, func(message []byte) error {
						if gjson.GetBytes(message, "type").String() == "response.completed" {
							terminal = append([]byte(nil), message...)
						}
						return nil
					})
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 10, result.Usage.InputTokens)
				require.Equal(t, 125, result.Usage.OutputTokens)
				require.Equal(t, int64(10), gjson.GetBytes(terminal, "response.usage.input_tokens").Int())
				require.Equal(t, int64(125), gjson.GetBytes(terminal, "response.usage.output_tokens").Int())
				require.Equal(t, int64(75), gjson.GetBytes(terminal, "response.usage.output_tokens_details.reasoning_tokens").Int())
				require.Equal(t, int64(135), gjson.GetBytes(terminal, "response.usage.total_tokens").Int())
			})
		}
	}
}

func TestOpenAIReasoningUsageTerminalReplacesProgressiveUsage(t *testing.T) {
	coefficient := 1.5
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIReasoningBillingMultiplier: &coefficient}}}
	account := &Account{Platform: PlatformOpenAI}
	usage := &OpenAIUsage{}
	svc.parseSSEUsageBytes(svc.repriceOpenAIReasoningBody(account, []byte(`{"type":"response.in_progress","usage":{"input_tokens":10,"output_tokens":100,"output_tokens_details":{"reasoning_tokens":50}}}`)), usage)
	require.Equal(t, 125, usage.OutputTokens)
	parseOpenAIWSResponseUsageFromCompletedEvent(svc.repriceOpenAIReasoningBody(account, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":80,"output_tokens_details":{"reasoning_tokens":0}}}}`)), usage)
	require.Equal(t, 80, usage.OutputTokens)
}

func TestOpenAIReasoningUsageBufferedSSEFrames(t *testing.T) {
	coefficient := 1.5
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIReasoningBillingMultiplier: &coefficient}}, toolCorrector: NewCodexToolCorrector()}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	payload := `{"response":` + strings.Replace(reasoningUsageResponse, `,"usage":`, ",\ndata:\"usage\":", 1) + `}`
	body := "event: response.completed\r\nid: turn-1\r\n: keepalive\r\ndata: " + payload
	for _, route := range []string{"responses", "passthrough", "chat", "messages"} {
		t.Run(route, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			var usage *OpenAIUsage
			var err error
			switch route {
			case "responses":
				var result *openaiNonStreamingResult
				result, err = svc.handleNonStreamingResponse(c.Request.Context(), resp, c, account, "gpt-5.1", "gpt-5.1")
				if result != nil {
					usage = result.OpenAIUsage
				}
			case "passthrough":
				var result *openaiNonStreamingResultPassthrough
				result, err = svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, account, "gpt-5.1", "gpt-5.1")
				if result != nil {
					usage = result.OpenAIUsage
				}
			default:
				var result *OpenAIForwardResult
				if route == "chat" {
					result, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
				} else {
					result, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
				}
				if result != nil {
					usage = &result.Usage
				}
			}
			require.NoError(t, err)
			require.NotNil(t, usage)
			require.Equal(t, 125, usage.OutputTokens)
			visible, ok := extractOpenAIUsageFromJSONBytes(rec.Body.Bytes())
			require.True(t, ok)
			require.Equal(t, 125, visible.OutputTokens)
		})
	}
	updated := string(svc.repriceOpenAIReasoningBody(account, []byte(body)))
	require.Contains(t, updated, "id: turn-1\r\n: keepalive\r\n")
}

func TestOpenAIReasoningUsagePassthroughPerTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := passthroughLifecycleConfig()
	coefficient := 1.5
	cfg.Gateway.OpenAIReasoningBillingMultiplier = &coefficient
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(cfg, upstream)
	turns := make(chan int, 2)
	server, finished := startPassthroughLifecycleServerWithHooks(t, ctx, svc, passthroughLifecycleAccount(), func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
			if err == nil && result != nil {
				turns <- result.Usage.OutputTokens
			}
		}}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = client.CloseNow() }()
	for index, frame := range []string{
		`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":100,"total_tokens":110,"output_tokens_details":{"reasoning_tokens":50}}}}`,
		`{"type":"response.completed","response":{"id":"resp_2","usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11,"output_tokens_details":{"reasoning_tokens":1}}}}`,
	} {
		if index > 0 {
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":"next"}`)))
		}
		requirePassthroughUpstreamWrite(t, upstream, time.Second)
		upstream.Send(frame)
		payload, err := readPassthroughLifecycleFrame(t, client, time.Second)
		require.NoError(t, err)
		wantOutput, wantReasoning := []int{125, 2}[index], []int{75, 2}[index]
		require.Equal(t, int64(wantOutput), gjson.GetBytes(payload, "response.usage.output_tokens").Int())
		require.Equal(t, int64(wantReasoning), gjson.GetBytes(payload, "response.usage.output_tokens_details.reasoning_tokens").Int())
		require.Equal(t, int64(10+wantOutput), gjson.GetBytes(payload, "response.usage.total_tokens").Int())
		select {
		case output := <-turns:
			require.Equal(t, wantOutput, output)
		case <-time.After(time.Second):
			t.Fatal("未收到本轮计费用量")
		}
	}
	_ = client.Close(coderws.StatusNormalClosure, "done")
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("WebSocket 会话未结束")
	}
}
