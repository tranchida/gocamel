module github.com/tranchida/gocamel/components/stomp

go 1.27.0

require (
	github.com/go-stomp/stomp v2.1.4+incompatible
	github.com/stretchr/testify v1.12.1
	github.com/tranchida/gocamel v1.0.0
)

require (
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

// v0.14.0 and v0.14.1 were tagged before the release commit and require
// core v0.13.1, which lacks APIs this module uses.
retract [v0.14.0, v0.14.1]
