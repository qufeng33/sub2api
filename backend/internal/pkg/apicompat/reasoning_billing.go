package apicompat

import (
	"math"
	"strconv"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// RepriceOpenAIReasoningUsage 将已计入输出总量的推理 Token 按系数向上取整，
// 同步调整输出量及已有的总量。每份上游响应只能在解析、转发前调用一次。
// 缺少有效推理明细或折算后超出整数范围时，保留该 usage 对象。
func RepriceOpenAIReasoningUsage(body []byte, multiplier float64) []byte {
	if multiplier == 1 || multiplier < 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || !gjson.ValidBytes(body) {
		return body
	}
	factor := decimal.NewFromFloat(multiplier)
	for _, path := range []string{"usage", "response.usage", "data.usage", "data.response.usage"} {
		usage := gjson.GetBytes(body, path)
		if !usage.IsObject() {
			continue
		}
		outputField, detailsField := "output_tokens", "output_tokens_details"
		if !usage.Get(outputField).Exists() {
			outputField, detailsField = "completion_tokens", "completion_tokens_details"
		}
		reasoningField := detailsField + ".reasoning_tokens"
		output, validOutput := reasoningBillingTokenCount(usage.Get(outputField))
		reasoning, validReasoning := reasoningBillingTokenCount(usage.Get(reasoningField))
		if !validOutput || !validReasoning || reasoning == 0 || reasoning > output {
			continue
		}
		if image := usage.Get(detailsField + ".image_tokens"); image.Exists() {
			imageTokens, validImage := reasoningBillingTokenCount(image)
			if !validImage || imageTokens > output-reasoning {
				continue
			}
		}
		scaled := decimal.NewFromInt(reasoning).Mul(factor).Ceil()
		// output_tokens 持久化为 32 位整数，折算不能产生无法入库的用量。
		if scaled.GreaterThan(decimal.NewFromInt(math.MaxInt32 - (output - reasoning))) {
			continue
		}
		newReasoning := scaled.IntPart()
		delta := newReasoning - reasoning
		if delta == 0 {
			continue
		}
		changes := []struct {
			field string
			value int64
		}{
			{outputField, output + delta},
			{reasoningField, newReasoning},
		}
		if total := usage.Get("total_tokens"); total.Exists() {
			tokens, valid := reasoningBillingTokenCount(total)
			if !valid || tokens < reasoning || (delta > 0 && tokens > math.MaxInt64-delta) {
				continue
			}
			changes = append(changes, struct {
				field string
				value int64
			}{"total_tokens", tokens + delta})
		}
		updated := body
		var err error
		for _, change := range changes {
			updated, err = sjson.SetBytes(updated, path+"."+change.field, change.value)
			if err != nil {
				break
			}
		}
		if err == nil {
			body = updated
		}
	}
	return body
}

func reasoningBillingTokenCount(value gjson.Result) (int64, bool) {
	if value.Type != gjson.Number {
		return 0, false
	}
	count, err := strconv.ParseInt(value.Raw, 10, 64)
	return count, err == nil && count >= 0
}
