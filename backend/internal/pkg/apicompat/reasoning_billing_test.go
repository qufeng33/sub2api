package apicompat

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRepriceOpenAIReasoningUsage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reasoning  int64
		multiplier float64
		want       int64
	}{
		{"surcharge", 50, 1.5, 75},
		{"discount", 50, 0.8, 40},
		{"free", 50, 0, 0},
		{"unchanged", 50, 1, 50},
		{"round_up", 1, 1.5, 2},
		{"small_discount", 1, 0.8, 1},
		{"decimal_exact", 100, 1.1, 110},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, shape := range []struct{ path, output, details string }{
				{"usage", "output_tokens", "output_tokens_details"},
				{"response.usage", "output_tokens", "output_tokens_details"},
				{"data.usage", "completion_tokens", "completion_tokens_details"},
				{"data.response.usage", "output_tokens", "output_tokens_details"},
			} {
				usage := fmt.Sprintf(`{"input_tokens":10,"%s":100,"total_tokens":110,"%s":{"reasoning_tokens":%d},"input_tokens_details":{"cached_tokens":3}}`, shape.output, shape.details, tc.reasoning)
				body := []byte(`{"usage":` + usage + `}`)
				switch shape.path {
				case "response.usage":
					body = []byte(`{"response":` + string(body) + `}`)
				case "data.usage":
					body = []byte(`{"data":` + string(body) + `}`)
				case "data.response.usage":
					body = []byte(`{"data":{"response":` + string(body) + `}}`)
				}
				got := gjson.GetBytes(RepriceOpenAIReasoningUsage(body, tc.multiplier), shape.path)
				require.Equal(t, tc.want, got.Get(shape.details+".reasoning_tokens").Int())
				require.Equal(t, 100-tc.reasoning+tc.want, got.Get(shape.output).Int())
				require.Equal(t, 110-tc.reasoning+tc.want, got.Get("total_tokens").Int())
				require.Equal(t, int64(3), got.Get("input_tokens_details.cached_tokens").Int())
			}
		})
	}
}

func TestRepriceOpenAIReasoningUsagePreservesInvalidOrUnrelatedData(t *testing.T) {
	for _, body := range []string{
		`{"usage":{"output_tokens":10}}`,
		`{"usage":{"output_tokens":10,"output_tokens_details":{"reasoning_tokens":11}}}`,
		`{"usage":{"output_tokens":10,"output_tokens_details":{"reasoning_tokens":5,"image_tokens":6}}}`,
		`{"usage":{"output_tokens":10,"output_tokens_details":{"reasoning_tokens":1.5}}}`,
		`{"usage":{"output_tokens":10,"output_tokens_details":{"reasoning_tokens":"5"}}}`,
		`{"usage":{"output_tokens":10,"total_tokens":9223372036854775807,"output_tokens_details":{"reasoning_tokens":5}}}`,
		`{"usage":{"output_tokens":2147483647,"output_tokens_details":{"reasoning_tokens":1}}}`,
		`{"text":"usage output_tokens reasoning_tokens"}`,
		`[DONE]`,
	} {
		require.Equal(t, body, string(RepriceOpenAIReasoningUsage([]byte(body), 1.5)))
	}
	body := []byte(`{"usage":{"output_tokens":10,"output_tokens_details":{"reasoning_tokens":5}}}`)
	for _, multiplier := range []float64{-1, math.NaN(), math.Inf(1), math.MaxFloat64} {
		require.Equal(t, body, RepriceOpenAIReasoningUsage(body, multiplier))
	}
	repriced := RepriceOpenAIReasoningUsage(body, 1.5)
	require.False(t, gjson.GetBytes(repriced, "usage.total_tokens").Exists())
}
