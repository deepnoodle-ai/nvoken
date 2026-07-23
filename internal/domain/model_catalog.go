package domain

type ModelProvider string

const (
	ModelProviderAnthropic ModelProvider = "anthropic"
	ModelProviderOpenAI    ModelProvider = "openai"
)

func (p ModelProvider) Valid() bool {
	switch p {
	case ModelProviderAnthropic, ModelProviderOpenAI:
		return true
	default:
		return false
	}
}

type ModelPricingStatus string

const (
	ModelPricingPriced   ModelPricingStatus = "priced"
	ModelPricingUnpriced ModelPricingStatus = "unpriced"
	ModelPricingUnknown  ModelPricingStatus = "unknown"
)

type ModelPricing struct {
	Provider       ModelProvider
	Model          string
	Status         ModelPricingStatus
	Currency       string
	Unit           string
	Input          string
	Output         string
	CacheRead      *string
	CacheWrite     *string
	UpdatedAt      string
	PricingVersion string
}

type ModelDescriptor struct {
	Provider            ModelProvider
	ID                  string
	Cataloged           bool
	DisplayName         string
	Description         string
	ContextWindowTokens *int
	MaxOutputTokens     *int
	InputModalities     []string
	Recommended         bool
	Deprecated          bool
	Pricing             ModelPricing
}

type ModelCatalog struct {
	Items   []ModelDescriptor
	Version string
}
