package divegen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/deepnoodle-ai/dive/llm"
	"github.com/deepnoodle-ai/dive/providers"
	"github.com/deepnoodle-ai/dive/providers/anthropic"
	"github.com/deepnoodle-ai/dive/providers/openai"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

const pricingUnit = "per_million_tokens"

type catalogEntry struct {
	provider            domain.ModelProvider
	id                  string
	displayName         string
	description         string
	contextWindowTokens *int
	maxOutputTokens     *int
	inputModalities     []string
	recommended         bool
	deprecated          bool
}

var modelCatalog = []catalogEntry{
	{
		provider:            domain.ModelProviderAnthropic,
		id:                  anthropic.ModelClaudeFable5,
		displayName:         "Claude Fable 5",
		description:         "Most capable Anthropic model for demanding reasoning and long-horizon work.",
		contextWindowTokens: intPointer(1_000_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
	},
	{
		provider:            domain.ModelProviderAnthropic,
		id:                  anthropic.ModelClaudeSonnet5,
		displayName:         "Claude Sonnet 5",
		description:         "Recommended Anthropic model for most agents.",
		contextWindowTokens: intPointer(1_000_000),
		inputModalities:     []string{"text"},
		recommended:         true,
	},
	{
		provider:            domain.ModelProviderAnthropic,
		id:                  anthropic.ModelClaudeOpus48,
		displayName:         "Claude Opus 4.8",
		description:         "Deep reasoning for complex agent workloads.",
		contextWindowTokens: intPointer(1_000_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
	},
	{
		provider:            domain.ModelProviderAnthropic,
		id:                  anthropic.ModelClaudeHaiku45,
		displayName:         "Claude Haiku 4.5",
		description:         "Fast, low-latency model for lightweight agents.",
		contextWindowTokens: intPointer(200_000),
		maxOutputTokens:     intPointer(64_000),
		inputModalities:     []string{"text"},
	},
	{
		provider:            domain.ModelProviderOpenAI,
		id:                  openai.ModelGPT56Sol,
		displayName:         "GPT-5.6 Sol",
		description:         "Most capable GPT-5.6 tier for complex reasoning and coding.",
		contextWindowTokens: intPointer(1_050_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
	},
	{
		provider:            domain.ModelProviderOpenAI,
		id:                  openai.ModelGPT56Terra,
		displayName:         "GPT-5.6 Terra",
		description:         "Balanced GPT-5.6 tier for intelligence and cost.",
		contextWindowTokens: intPointer(1_050_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
	},
	{
		provider:            domain.ModelProviderOpenAI,
		id:                  openai.ModelGPT56Luna,
		displayName:         "GPT-5.6 Luna",
		description:         "Recommended OpenAI model for efficient, high-volume agents.",
		contextWindowTokens: intPointer(1_050_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
		recommended:         true,
	},
	{
		provider:            domain.ModelProviderOpenAI,
		id:                  openai.ModelGPT55,
		displayName:         "GPT-5.5",
		description:         "Previous flagship OpenAI model.",
		contextWindowTokens: intPointer(1_050_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
	},
	{
		provider:            domain.ModelProviderOpenAI,
		id:                  openai.ModelGPT54,
		displayName:         "GPT-5.4",
		description:         "More affordable frontier model for professional work.",
		contextWindowTokens: intPointer(1_050_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
	},
	{
		provider:            domain.ModelProviderOpenAI,
		id:                  openai.ModelGPT54Mini,
		displayName:         "GPT-5.4 Mini",
		description:         "Fast, lower-cost model for coding and subagents.",
		contextWindowTokens: intPointer(400_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
	},
	{
		provider:            domain.ModelProviderOpenAI,
		id:                  openai.ModelGPT54Nano,
		displayName:         "GPT-5.4 Nano",
		description:         "Lowest-cost GPT-5.4-class model.",
		contextWindowTokens: intPointer(400_000),
		maxOutputTokens:     intPointer(128_000),
		inputModalities:     []string{"text"},
	},
}

var (
	catalogOnce        sync.Once
	catalogDescriptors []domain.ModelDescriptor
	catalogVersion     string
	catalogIndex       map[string]domain.ModelDescriptor
	pricingVersions    map[domain.ModelProvider]string
)

func intPointer(value int) *int {
	return &value
}

func initializeModelCatalog() {
	if err := validateModelCatalog(modelCatalog); err != nil {
		panic("invalid embedded model catalog: " + err.Error())
	}
	if err := validateInstalledPricingTables(); err != nil {
		panic("invalid installed model pricing: " + err.Error())
	}
	pricingVersions = make(map[domain.ModelProvider]string, 2)
	for _, provider := range []domain.ModelProvider{
		domain.ModelProviderAnthropic,
		domain.ModelProviderOpenAI,
	} {
		pricingVersions[provider] = pricingVersion(provider)
	}
	catalogDescriptors = make([]domain.ModelDescriptor, 0, len(modelCatalog))
	catalogIndex = make(map[string]domain.ModelDescriptor, len(modelCatalog))
	for _, entry := range modelCatalog {
		descriptor := domain.ModelDescriptor{
			Provider:            entry.provider,
			ID:                  entry.id,
			Cataloged:           true,
			DisplayName:         entry.displayName,
			Description:         entry.description,
			ContextWindowTokens: entry.contextWindowTokens,
			MaxOutputTokens:     entry.maxOutputTokens,
			InputModalities:     append([]string(nil), entry.inputModalities...),
			Recommended:         entry.recommended,
			Deprecated:          entry.deprecated,
			Pricing:             resolveModelPricing(entry.provider, entry.id, pricingVersions[entry.provider]),
		}
		catalogDescriptors = append(catalogDescriptors, descriptor)
		catalogIndex[catalogKey(entry.provider, entry.id)] = descriptor
	}
	sort.Slice(catalogDescriptors, func(i, j int) bool {
		if catalogDescriptors[i].Provider != catalogDescriptors[j].Provider {
			return catalogDescriptors[i].Provider < catalogDescriptors[j].Provider
		}
		return catalogDescriptors[i].ID < catalogDescriptors[j].ID
	})
	catalogVersion = contentVersion(catalogDescriptors)
}

func validateModelCatalog(entries []catalogEntry) error {
	seen := make(map[string]struct{}, len(entries))
	recommendations := make(map[domain.ModelProvider]int, 2)
	providersSeen := make(map[domain.ModelProvider]struct{}, 2)
	for index, entry := range entries {
		if !entry.provider.Valid() {
			return fmt.Errorf("entry %d has unsupported provider %q", index, entry.provider)
		}
		providersSeen[entry.provider] = struct{}{}
		if entry.id == "" ||
			!utf8.ValidString(entry.id) ||
			strings.TrimSpace(entry.id) != entry.id ||
			utf8.RuneCountInString(entry.id) > 255 {
			return fmt.Errorf("entry %d has invalid model id %q", index, entry.id)
		}
		key := catalogKey(entry.provider, entry.id)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate model catalog key %q/%q", entry.provider, entry.id)
		}
		seen[key] = struct{}{}
		if entry.displayName == "" || entry.description == "" {
			return fmt.Errorf("cataloged model %q/%q is missing display metadata", entry.provider, entry.id)
		}
		if entry.contextWindowTokens != nil && *entry.contextWindowTokens <= 0 {
			return fmt.Errorf("cataloged model %q/%q has an invalid context window", entry.provider, entry.id)
		}
		if entry.maxOutputTokens != nil && *entry.maxOutputTokens <= 0 {
			return fmt.Errorf("cataloged model %q/%q has an invalid output limit", entry.provider, entry.id)
		}
		if len(entry.inputModalities) == 0 {
			return fmt.Errorf("cataloged model %q/%q has no input modalities", entry.provider, entry.id)
		}
		modalities := make(map[string]struct{}, len(entry.inputModalities))
		for _, modality := range entry.inputModalities {
			if modality != "text" && modality != "image" {
				return fmt.Errorf("cataloged model %q/%q has unsupported input modality %q", entry.provider, entry.id, modality)
			}
			if _, ok := modalities[modality]; ok {
				return fmt.Errorf("cataloged model %q/%q repeats input modality %q", entry.provider, entry.id, modality)
			}
			modalities[modality] = struct{}{}
		}
		if entry.deprecated && entry.recommended {
			return fmt.Errorf("deprecated model %q/%q cannot be recommended", entry.provider, entry.id)
		}
		if entry.recommended {
			recommendations[entry.provider]++
		}
		if !providerPricingContains(entry.provider, entry.id) {
			return fmt.Errorf("cataloged model %q/%q is not backed by its provider pricing table", entry.provider, entry.id)
		}
	}
	for provider := range providersSeen {
		if recommendations[provider] != 1 {
			return fmt.Errorf("provider %q has %d active recommended models; want exactly one", provider, recommendations[provider])
		}
	}
	return nil
}

func validateInstalledPricingTables() error {
	seen := make(map[string]domain.ModelProvider)
	for _, provider := range []domain.ModelProvider{
		domain.ModelProviderAnthropic,
		domain.ModelProviderOpenAI,
	} {
		table := providerPricingTable(provider)
		for model, direct := range table {
			if other, ok := seen[model]; ok {
				return fmt.Errorf("model id %q has pricing under both %q and %q", model, other, provider)
			}
			seen[model] = provider
			central, ok := providers.PricingFor(model, false)
			if !ok {
				return fmt.Errorf("%q/%q pricing is missing from standard cost enforcement", provider, model)
			}
			expected := providerStandardPricing(provider, direct)
			if !equalPricing(expected, central) {
				return fmt.Errorf("%q/%q pricing differs from standard cost enforcement", provider, model)
			}
		}
	}
	return nil
}

func providerPricingTable(provider domain.ModelProvider) map[string]llm.PricingInfo {
	switch provider {
	case domain.ModelProviderAnthropic:
		return anthropic.TextModelPricing
	case domain.ModelProviderOpenAI:
		return openai.TextModelPricing
	default:
		return nil
	}
}

func providerStandardPricing(
	provider domain.ModelProvider,
	pricing llm.PricingInfo,
) llm.PricingInfo {
	if provider == domain.ModelProviderAnthropic {
		if pricing.CacheReadPrice == 0 {
			pricing.CacheReadPrice = pricing.InputPrice * 0.10
		}
		if pricing.CacheWritePrice == 0 {
			pricing.CacheWritePrice = pricing.InputPrice * 1.25
		}
	}
	return pricing
}

func equalPricing(left, right llm.PricingInfo) bool {
	return left.Model == right.Model &&
		left.InputPrice == right.InputPrice &&
		left.OutputPrice == right.OutputPrice &&
		left.CacheReadPrice == right.CacheReadPrice &&
		left.CacheWritePrice == right.CacheWritePrice &&
		strings.EqualFold(left.Currency, right.Currency) &&
		left.UpdatedAt == right.UpdatedAt
}

func (g *Generator) ListModels(
	provider domain.ModelProvider,
	includeDeprecated bool,
) domain.ModelCatalog {
	catalogOnce.Do(initializeModelCatalog)
	items := make([]domain.ModelDescriptor, 0, len(catalogDescriptors))
	for _, descriptor := range catalogDescriptors {
		if provider != "" && descriptor.Provider != provider {
			continue
		}
		if descriptor.Deprecated && !includeDeprecated {
			continue
		}
		items = append(items, cloneDescriptor(descriptor))
	}
	return domain.ModelCatalog{Items: items, Version: catalogVersion}
}

func (g *Generator) ResolveModel(
	provider domain.ModelProvider,
	model string,
) domain.ModelDescriptor {
	catalogOnce.Do(initializeModelCatalog)
	if descriptor, ok := catalogIndex[catalogKey(provider, model)]; ok {
		return cloneDescriptor(descriptor)
	}
	return domain.ModelDescriptor{
		Provider:  provider,
		ID:        model,
		Cataloged: false,
		Pricing:   resolveModelPricing(provider, model, pricingVersions[provider]),
	}
}

func (g *Generator) ResolveModelPricing(provider, model string) domain.ModelPricing {
	catalogOnce.Do(initializeModelCatalog)
	canonical := domain.ModelProvider(provider)
	return resolveModelPricing(canonical, model, pricingVersions[canonical])
}

func resolveModelPricing(
	provider domain.ModelProvider,
	model string,
	version string,
) domain.ModelPricing {
	capability := domain.ModelPricing{
		Provider:       provider,
		Model:          model,
		Status:         domain.ModelPricingUnknown,
		PricingVersion: version,
	}
	if !provider.Valid() {
		return capability
	}
	if version == "" {
		capability.PricingVersion = contentVersion(struct {
			Provider domain.ModelProvider `json:"provider"`
		}{Provider: provider})
	}
	if !providerPricingContains(provider, model) {
		capability.Status = domain.ModelPricingUnpriced
		return capability
	}
	pricing, ok := providers.PricingFor(model, false)
	if !ok || !strings.EqualFold(pricing.Currency, "USD") || !validPricing(pricing) {
		capability.Status = domain.ModelPricingUnpriced
		return capability
	}
	capability.Status = domain.ModelPricingPriced
	capability.Currency = "USD"
	capability.Unit = pricingUnit
	capability.Input = decimalString(pricing.InputPrice)
	capability.Output = decimalString(pricing.OutputPrice)
	if pricing.CacheReadPrice != 0 {
		value := decimalString(pricing.CacheReadPrice)
		capability.CacheRead = &value
	}
	if pricing.CacheWritePrice != 0 {
		value := decimalString(pricing.CacheWritePrice)
		capability.CacheWrite = &value
	}
	capability.UpdatedAt = pricing.UpdatedAt
	return capability
}

func providerPricingContains(provider domain.ModelProvider, model string) bool {
	switch provider {
	case domain.ModelProviderAnthropic:
		_, ok := anthropic.TextModelPricing[model]
		return ok
	case domain.ModelProviderOpenAI:
		_, ok := openai.TextModelPricing[model]
		return ok
	default:
		return false
	}
}

func validPricing(pricing llm.PricingInfo) bool {
	return finiteNonnegative(pricing.InputPrice) &&
		finiteNonnegative(pricing.OutputPrice) &&
		finiteNonnegative(pricing.CacheReadPrice) &&
		finiteNonnegative(pricing.CacheWritePrice) &&
		pricing.UpdatedAt != ""
}

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func decimalString(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func pricingVersion(provider domain.ModelProvider) string {
	type pricingRecord struct {
		Model      string `json:"model"`
		Currency   string `json:"currency"`
		Unit       string `json:"unit"`
		Input      string `json:"input"`
		Output     string `json:"output"`
		CacheRead  string `json:"cache_read,omitempty"`
		CacheWrite string `json:"cache_write,omitempty"`
		UpdatedAt  string `json:"updated_at"`
	}
	records := make([]pricingRecord, 0)
	table := providerPricingTable(provider)
	if table == nil {
		return ""
	}
	for model := range table {
		pricing, ok := providers.PricingFor(model, false)
		if !ok {
			continue
		}
		records = append(records, pricingRecord{
			Model:      model,
			Currency:   strings.ToUpper(pricing.Currency),
			Unit:       pricingUnit,
			Input:      decimalString(pricing.InputPrice),
			Output:     decimalString(pricing.OutputPrice),
			CacheRead:  decimalString(pricing.CacheReadPrice),
			CacheWrite: decimalString(pricing.CacheWritePrice),
			UpdatedAt:  pricing.UpdatedAt,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Model < records[j].Model })
	return contentVersion(struct {
		Provider domain.ModelProvider `json:"provider"`
		Records  []pricingRecord      `json:"records"`
	}{Provider: provider, Records: records})
}

func contentVersion(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(errors.New("encode model catalog version: " + err.Error()))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:16])
}

func catalogKey(provider domain.ModelProvider, model string) string {
	return string(provider) + "\x00" + model
}

func cloneDescriptor(value domain.ModelDescriptor) domain.ModelDescriptor {
	value.InputModalities = append([]string(nil), value.InputModalities...)
	if value.ContextWindowTokens != nil {
		value.ContextWindowTokens = intPointer(*value.ContextWindowTokens)
	}
	if value.MaxOutputTokens != nil {
		value.MaxOutputTokens = intPointer(*value.MaxOutputTokens)
	}
	if value.Pricing.CacheRead != nil {
		cacheRead := *value.Pricing.CacheRead
		value.Pricing.CacheRead = &cacheRead
	}
	if value.Pricing.CacheWrite != nil {
		cacheWrite := *value.Pricing.CacheWrite
		value.Pricing.CacheWrite = &cacheWrite
	}
	return value
}
