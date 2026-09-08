package service

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
)

func (s *OpenAIGatewayService) openAIReasoningBillingMultiplier(account *Account) float64 {
	if account == nil || account.Platform != PlatformOpenAI || s == nil || s.cfg == nil || s.cfg.Gateway.OpenAIReasoningBillingMultiplier == nil {
		return 1
	}
	return *s.cfg.Gateway.OpenAIReasoningBillingMultiplier
}

func (s *OpenAIGatewayService) repriceOpenAIReasoningBody(account *Account, body []byte) []byte {
	multiplier := s.openAIReasoningBillingMultiplier(account)
	if multiplier == 1 {
		return body
	}
	if !bodyHasSSEFraming(body) {
		return apicompat.RepriceOpenAIReasoningUsage(body, multiplier)
	}
	// 非流式请求也可能收到 SSE；按完整事件处理多行 data，保留其他事件字段。
	lines := strings.SplitAfter(string(body), "\n")
	var positions []int
	var data []string
	flush := func() {
		if len(positions) == 0 {
			return
		}
		payload := []byte(strings.Join(data, "\n"))
		updated := apicompat.RepriceOpenAIReasoningUsage(payload, multiplier)
		if !bytes.Equal(payload, updated) {
			var compact bytes.Buffer
			if json.Compact(&compact, updated) == nil {
				ending := ""
				if strings.HasSuffix(lines[positions[0]], "\n") {
					ending = "\n"
				}
				lines[positions[0]] = "data: " + compact.String() + ending
				for _, index := range positions[1:] {
					lines[index] = ""
				}
			}
		}
		positions, data = nil, nil
	}
	for index, line := range lines {
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			flush()
		} else if payload, ok := extractOpenAISSEDataLine(line); ok {
			positions = append(positions, index)
			data = append(data, payload)
		}
	}
	flush()
	return []byte(strings.Join(lines, ""))
}

func (s *OpenAIGatewayService) repriceOpenAIReasoningSSELine(account *Account, line string) string {
	multiplier := s.openAIReasoningBillingMultiplier(account)
	if multiplier == 1 {
		return line
	}
	if payload, ok := extractOpenAISSEDataLine(line); ok {
		updated := apicompat.RepriceOpenAIReasoningUsage([]byte(payload), multiplier)
		if string(updated) != payload {
			return "data: " + string(updated)
		}
	}
	return line
}

type openAIReasoningFrameConn struct {
	openaiwsv2.FrameConn
	multiplier float64
}

func (c *openAIReasoningFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	messageType, payload, err := c.FrameConn.ReadFrame(ctx)
	if err == nil && messageType == coderws.MessageText {
		payload = apicompat.RepriceOpenAIReasoningUsage(payload, c.multiplier)
	}
	return messageType, payload, err
}
