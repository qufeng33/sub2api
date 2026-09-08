package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOpenAIReasoningBillingMultiplier(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  float64
	}{
		{"", 1}, {"0", 0}, {"0.75", 0.75}, {"1.5", 1.5}, {"1.333333", 1.333333},
	} {
		t.Run(tc.value, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("GATEWAY_OPENAI_REASONING_BILLING_MULTIPLIER", tc.value)
			cfg, err := Load()
			require.NoError(t, err)
			require.NotNil(t, cfg.Gateway.OpenAIReasoningBillingMultiplier)
			require.Equal(t, tc.want, *cfg.Gateway.OpenAIReasoningBillingMultiplier)
		})
	}
}

func TestLoadOpenAIReasoningBillingMultiplierRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"-0.1", "NaN", "+Inf", "-Inf", "invalid"} {
		t.Run(value, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("GATEWAY_OPENAI_REASONING_BILLING_MULTIPLIER", value)
			_, err := Load()
			require.Error(t, err)
		})
	}
}
