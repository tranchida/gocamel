module gitlab.com/tranchida/gocamel/components/openai

go 1.25.0

// The replace directive below makes the in-repo core the source of truth
// during development. It must be dropped (and the require pinned to a real
// tag) by the release script once the core module is tagged.
require gitlab.com/tranchida/gocamel v0.0.0

require github.com/sashabaranov/go-openai v1.41.2

replace gitlab.com/tranchida/gocamel => ../..
