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

## License

See [LICENSE](LICENSE). Third-party software is credited in
[CREDITS.md](CREDITS.md). Each project folder also has its own `LICENSE` and
`CREDITS.md`.
