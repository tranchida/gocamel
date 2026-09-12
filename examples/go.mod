module gitlab.com/tranchida/gocamel/examples

go 1.27.0

// Examples always build against the in-repo modules.
require (
	gitlab.com/tranchida/gocamel v0.13.1
	gitlab.com/tranchida/gocamel/components/file v0.13.1
	gitlab.com/tranchida/gocamel/components/mail v0.13.1
	gitlab.com/tranchida/gocamel/observability/otel v0.13.1
	gitlab.com/tranchida/gocamel/observability/prometheus v0.13.1
)

require (
	github.com/prometheus/client_golang v1.23.2
	go.opentelemetry.io/otel v1.43.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.43.0
	go.opentelemetry.io/otel/sdk v1.43.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/emersion/go-imap/v2 v2.0.0-beta.8 // indirect
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/knadh/go-pop3 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/otel/trace v1.43.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
)

replace (
	gitlab.com/tranchida/gocamel => ..
	gitlab.com/tranchida/gocamel/components/file => ../components/file
	gitlab.com/tranchida/gocamel/components/mail => ../components/mail
	gitlab.com/tranchida/gocamel/observability/otel => ../observability/otel
	gitlab.com/tranchida/gocamel/observability/prometheus => ../observability/prometheus
)
