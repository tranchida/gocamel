module gitlab.com/tranchida/gocamel/components/smb

go 1.25.0

// The replace directive below makes the in-repo core the source of truth
// during development. It must be dropped (and the require pinned to a real
// tag) by the release script once the core module is tagged.
require gitlab.com/tranchida/gocamel v0.0.0

require github.com/hirochachacha/go-smb2 v1.1.0

require (
	github.com/geoffgarside/ber v1.1.0 // indirect
	golang.org/x/crypto v0.51.0 // indirect
)

replace gitlab.com/tranchida/gocamel => ../..
