module gitlab.com/tranchida/gocamel/components/nats

go 1.25.0

// The replace directive below makes the in-repo core the source of truth
// during development. It must be dropped (and the require pinned to a real
// tag) by the release script once the core module is tagged.
require gitlab.com/tranchida/gocamel v0.0.0

require (
	github.com/nats-io/nats-server/v2 v2.14.1
	github.com/nats-io/nats.go v1.52.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/antithesishq/antithesis-sdk-go v0.7.0-default-no-op // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/jwt/v2 v2.8.1 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/crypto v0.51.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace gitlab.com/tranchida/gocamel => ../..
