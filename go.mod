module github.com/gallowaysoftware/textbook-to-audiobook

go 1.26.3

require (
	github.com/gallowaysoftware/vibe v0.6.0
	github.com/spf13/cobra v1.10.2
)

require (
	connectrpc.com/connect v1.19.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.36.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// Local development: pin to the in-tree vibe checkout so the new
// Pipeline.Requirements() / vamp.BuildRoot API surface (added after
// v0.5.0 was tagged) is available. Drop this replace once the
// dependency tag containing those APIs is published.
replace github.com/gallowaysoftware/vibe => ../vibe
