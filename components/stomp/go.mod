module gitlab.com/tranchida/gocamel/components/stomp

go 1.27.0

require (
	github.com/go-stomp/stomp v2.1.4+incompatible
	github.com/stretchr/testify v1.11.1
	gitlab.com/tranchida/gocamel v0.16.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// v0.14.0 and v0.14.1 were tagged before the release commit and require
// core v0.13.1, which lacks APIs this module uses.
retract [v0.14.0, v0.14.1]
