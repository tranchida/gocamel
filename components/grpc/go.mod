module github.com/tranchida/gocamel/components/grpc

go 1.27.0

require (
	github.com/stretchr/testify v1.12.1
	github.com/tranchida/gocamel v1.0.0
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
)

// v0.14.0 and v0.14.1 were tagged before the release commit and require
// core v0.13.1, which lacks APIs this module uses.
retract [v0.14.0, v0.14.1]
