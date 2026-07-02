module gitlab.com/tranchida/gocamel/components/telegram

go 1.25.0

// The replace directive below makes the in-repo core the source of truth
// during development. It must be dropped (and the require pinned to a real
// tag) by the release script once the core module is tagged.
require gitlab.com/tranchida/gocamel v0.0.0

require github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1

replace gitlab.com/tranchida/gocamel => ../..
