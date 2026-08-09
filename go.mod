module github.com/deepnoodle-ai/nvoken

go 1.26.5

require (
	github.com/deepnoodle-ai/nvoken/sdk/go v0.0.0
	github.com/deepnoodle-ai/wonton v0.0.37
	github.com/oapi-codegen/runtime v1.6.0
	github.com/pelletier/go-toml/v2 v2.4.3
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/alecthomas/chroma/v2 v2.20.0 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/yuin/goldmark v1.7.13 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/term v0.38.0 // indirect
)

replace github.com/deepnoodle-ai/nvoken/sdk/go => ./sdk/go
