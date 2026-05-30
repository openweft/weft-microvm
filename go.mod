module github.com/openweft/weft-microvm

go 1.25.1

require (
	github.com/cloud-boot/init v0.0.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.0
	github.com/openweft/weft-client v0.0.0
	github.com/openweft/weft-microvm-init v0.0.0
	github.com/openweft/weft-proto v0.0.0
	google.golang.org/grpc v1.81.1
)

require (
	github.com/agext/levenshtein v1.2.1 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/grpc-transports/ssh v0.0.0-00010101000000-000000000000 // indirect
	github.com/hashicorp/hcl/v2 v2.24.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/zclconf/go-cty v1.18.1 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/mod v0.34.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// Local sibling checkouts — until the repos are published.
replace github.com/openweft/weft-client => ../weft-client

replace github.com/openweft/weft-proto => ../weft-proto

replace github.com/openweft/weft-microvm-init => ../weft-microvm-init

replace github.com/cloud-boot/init => ../../cloud-boot/init

replace github.com/grpc-transports/ssh => ../../grpc-transports/ssh

// Transitive local modules pulled in via cloud-boot/init and
// weft-client (Go honours only the main module's replace block).
replace github.com/grpc-transports/wireguard => ../../grpc-transports/wireguard

replace github.com/go-coff/peln => ../../go-coff/peln

replace github.com/go-filesystems/interface => ../../go-filesystems/interface

replace github.com/go-filesystems/ext4 => ../../go-filesystems/ext4

replace github.com/go-filesystems/xfs => ../../go-filesystems/xfs

replace github.com/go-filesystems/btrfs => ../../go-filesystems/btrfs

replace github.com/go-filesystems/zfs => ../../go-filesystems/zfs

replace github.com/go-fde/luks => ../../go-fde/luks

replace github.com/go-crypto/zfscrypt => ../../go-crypto/zfscrypt

replace github.com/go-crypto/ccm => ../../go-crypto/ccm

replace github.com/go-bootloaders/systemd-boot => ../../go-bootloaders/systemd-boot
