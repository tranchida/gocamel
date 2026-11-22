module github.com/tranchida/gocamel/components/kafka

go 1.27.0

require (
	github.com/segmentio/kafka-go v0.4.47
	github.com/stretchr/testify v1.12.1
	github.com/tranchida/gocamel v1.0.0
)

require (
	github.com/klauspost/compress v1.20.4 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)

// v0.14.0 and v0.14.1 were tagged before the release commit and require
// core v0.13.1, which lacks APIs this module uses.
retract [v0.14.0, v0.14.1]
