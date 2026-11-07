# sentinel-solana

Monorepo for Sentinel dVPN on Solana. Each top-level folder is a copy of a
separate upstream repository and is kept in sync with it.

## Projects

| Folder | Description | Language | Upstream |
|---|---|---|---|
| [sentinel-dvpn](sentinel-dvpn) | Command-line client (`sentinel-dvpn`) that manages sessions through the client daemon | Go | [sentinel-official/sentinel-dvpncli](https://github.com/sentinel-official/sentinel-dvpncli) |
| [sentinel-dvpnd](sentinel-dvpnd) | Sentinel dVPN daemon (`sentinel-dvpnd`) | Go | [sentinel-official/sentinel-dvpnx](https://github.com/sentinel-official/sentinel-dvpnx) |
| [sentinel-go-sdk](sentinel-go-sdk) | Go SDK with the VPN protocol integrations (WireGuard, AmneziaWG, OpenVPN, V2Ray, Xray, Hysteria2), node and provider APIs, and the on-chain program client | Go | [sentinel-official/sentinel-go-sdk](https://github.com/sentinel-official/sentinel-go-sdk) |
| [sentinel-programs](sentinel-programs) | Anchor programs `dvpn` and `distributor`, their IDLs, the generated Go client, and the launch tooling (`ceremony`, `xtask`) | Rust, Go | [sentinel-official/sentinel-programs](https://github.com/sentinel-official/sentinel-programs) |
| [sentinel-snapshot](sentinel-snapshot) | Chain snapshot tooling (`snapshot` with `snapshot`, `verify` and `prune` commands, and `anchorgen`) | Go | [sentinel-official/sentinel-snapshot](https://github.com/sentinel-official/sentinel-snapshot) |

## Requirements

- Go 1.27.3
- For `sentinel-programs`: Rust 1.97.1, Anchor 1.2.2 and Solana (Agave) 4.3.2

## Build and test

`sentinel-dvpn`, `sentinel-dvpnd` and `sentinel-go-sdk`:

```sh
make build
make test
```

`sentinel-snapshot`:

```sh
go build ./...
go test ./...
```

`sentinel-programs`:

```sh
anchor build   # also copies the IDLs to idl/ and regenerates the Go client
cargo test
```

### Docker images

`sentinel-dvpn` and `sentinel-dvpnd` each ship a `Dockerfile`. On a host whose primary
interface MTU is below 1500 — most cloud VMs (Google Cloud and others default to 1460), and
any host behind a VPN or an overlay network — `docker build` can hang on the `apk add` step.
Docker's default bridge network is MTU 1500, so the TLS fetch to the Alpine mirror sends
full-size packets the smaller uplink drops; when the path also filters ICMP "fragmentation
needed" (common on cloud networks) the sender never learns to shrink them and the download
stalls until it times out. Check the uplink's MTU with `ip -o link show` (the `mtu NNNN` on,
e.g., `eth0`).

Build with a network MTU that matches the host so the fetch fits:

```sh
# one-off: build on the host network, which already has the correct MTU
docker build --network=host -f sentinel-dvpnd/Dockerfile -t sentinel-dvpnd .

# or set the default-bridge MTU once in /etc/docker/daemon.json, then restart docker:
#   { "mtu": 1460 }

# with buildx, create a builder that uses the host network:
#   docker buildx create --name lowmtu --driver-opt network=host --use
```

## License

See [LICENSE](LICENSE). Third-party software is credited in
[CREDITS.md](CREDITS.md). Each project folder also has its own `LICENSE` and
`CREDITS.md`.
