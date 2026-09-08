package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIReasoningBillingMultiplierScope(t *testing.T) {
	coefficient := 1.5
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIReasoningBillingMultiplier: &coefficient}}}
	require.Equal(t, 1.5, svc.openAIReasoningBillingMultiplier(&Account{Platform: PlatformOpenAI}))
	for _, account := range []*Account{nil, {Platform: PlatformGrok}, {Platform: PlatformAnthropic}, {Platform: PlatformGemini}} {
		require.Equal(t, 1.0, svc.openAIReasoningBillingMultiplier(account))
	}
	require.Equal(t, 1.0, (&OpenAIGatewayService{}).openAIReasoningBillingMultiplier(&Account{Platform: PlatformOpenAI}))
}

func TestOpenAIReasoningBillingUsesRepricedUsage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		platform    string
		coefficient float64
		wantOutput  int
		freeFast    bool
	}{
		{"discount", PlatformOpenAI, 0.8, 90, false},
		{"surcharge", PlatformOpenAI, 1.5, 125, false},
		{"free_reasoning", PlatformOpenAI, 0, 50, false},
		{"unchanged", PlatformOpenAI, 1, 100, false},
		{"grok", PlatformGrok, 1.5, 100, false},
		{"free_fast_surcharge", PlatformOpenAI, 1.5, 125, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.cfg.Gateway.OpenAIReasoningBillingMultiplier = &tc.coefficient
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groupID := int64(10)
			inputPrice, outputPrice := 0.001, 0.002
			apiKey := &APIKey{ID: 2, GroupID: &groupID, Quota: 100, Group: &Group{
				ID: groupID, Platform: tc.platform, Status: StatusActive, Hydrated: true, RateMultiplier: 0.6,
				ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-5.1"}, BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice}},
			}}
			account := &Account{ID: 3, Platform: tc.platform, Type: AccountTypeAPIKey, Extra: map[string]any{"quota_limit": 100.0}}
			usage, ok := extractOpenAIUsageFromJSONBytes(svc.repriceOpenAIReasoningBody(account, []byte(reasoningUsageResponse)))
			require.True(t, ok)
			result := &OpenAIForwardResult{RequestID: "resp_reasoning", Model: "gpt-5.1", Usage: usage}
			wantOutputCost := float64(tc.wantOutput) * outputPrice
			wantTotal := 10*inputPrice + wantOutputCost
			wantActual := wantTotal * 0.6
			if tc.freeFast {
				priority, fastMultiplier := "priority", 3.0
				result.ServiceTier = &priority
				apiKey.Group.FreeOpenAIFast = true
				apiKey.Group.ModelPricing[0].FastMultiplier = &fastMultiplier
				wantTotal *= fastMultiplier
				wantOutputCost *= fastMultiplier
			}
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: result, APIKey: apiKey, User: &User{ID: 1},
				APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
				Account:       account,
			})
			require.NoError(t, err)
			require.Equal(t, usage, result.Usage)
			log := usageRepo.lastLog
			require.NotNil(t, log)
			require.Equal(t, tc.wantOutput, log.OutputTokens)
			require.InDelta(t, wantTotal, log.TotalCost, 1e-12)
			require.InDelta(t, wantOutputCost, log.OutputCost, 1e-12)
			require.Equal(t, 0.6, log.RateMultiplier)
			require.InDelta(t, wantActual, log.ActualCost, 1e-12)
			require.InDelta(t, wantTotal, billingRepo.lastCmd.AccountQuotaCost, 1e-10)
			require.InDelta(t, wantActual, billingRepo.lastCmd.BalanceCost, 1e-10)
			require.InDelta(t, wantActual, billingRepo.lastCmd.APIKeyQuotaCost, 1e-10)
		})
	}
}
