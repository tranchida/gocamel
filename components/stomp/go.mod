module github.com/tranchida/gocamel/components/stomp

go 1.27.0

require (
	github.com/go-stomp/stomp v2.1.4+incompatible
	github.com/stretchr/testify v1.12.1
	github.com/tranchida/gocamel v0.20.0
)

require (
	github.com/kr/text v0.2.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.50.0 // indirect
)

// v0.14.0 and v0.14.1 were tagged before the release commit and require
// core v0.13.1, which lacks APIs this module uses.
retract [v0.14.0, v0.14.1]
