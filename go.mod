module github.com/openweft/weft-microvm

go 1.27.2

require (
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/openweft/weft-client v0.0.0
	github.com/openweft/weft-microvm-init v0.0.0
	github.com/openweft/weft-proto v0.14.0
	google.golang.org/grpc v1.84.0
	oras.land/oras-go/v2 v2.6.2
)

require (
	github.com/agext/levenshtein v1.2.1 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/grpc-transports/ssh v0.2.0 // indirect
	github.com/hashicorp/hcl/v2 v2.24.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/zclconf/go-cty v1.18.1 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// Local sibling checkouts — until the repos are published.
replace github.com/openweft/weft-client => ../weft-client

replace github.com/openweft/weft-proto => ../weft-proto

replace github.com/openweft/weft-microvm-init => ../weft-microvm-init

replace github.com/grpc-transports/ssh => ../../grpc-transports/ssh

// Transitive local module pulled in via weft-client (Go honours only the
// main module's replace block).
replace github.com/grpc-transports/wireguard => ../../grpc-transports/wireguard
