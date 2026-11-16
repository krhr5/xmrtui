# xmrtui

A pixel-styled terminal UI for a Monero wallet. It runs `monero-wallet-rpc`
(installed with the `monero` package) in the background and talks to it over
localhost JSON-RPC with random, one-time digest-auth credentials.

Requires the Monero CLI tools (`monero-wallet-rpc`), e.g. `pacman -S monero` on Arch.

## Install

```
go install github.com/krhr5/xmrtui@latest
```

or build from source:

```
go build -o xmrtui .
./xmrtui -wallet ~/my-wallet            # path without .keys
./xmrtui -wallet ~/my-wallet -daemon node.example.com:18081
```

If `-wallet` is omitted and there is exactly one `*.keys` file in `~` or
`~/Monero/wallets/*/`, that one is used. `XMRTUI_WALLET` / `XMRTUI_DAEMON`
env vars work too.

Close `monero-wallet-cli` first — a wallet can only be open in one program.

### App launcher (Omarchy)

```
omarchy tui install "Monero Wallet" /path/to/xmrtui/launch.sh tile /path/to/xmrtui/icon.png
```

`launch.sh` keeps the window open if xmrtui exits with an error.

## Keys

| key | action |
|-----|--------|
| `1`–`5`, `tab`, `←→` | switch tab |
| `r` | refresh from node now |
| `c` | copy address / txid |
| `n` | new subaddress (Receive) |
| `v` | toggle QR code (Receive) |
| `enter` | fill in form (Send), details (History), change node (Node) |
| `q` | save & quit |

## Safety

- Sending is two-step: the transaction is built with `do_not_relay`, you see
  the exact fee and total, and nothing is broadcast until you press `Y`.
- The daemon is always treated as **untrusted**.
- RPC binds to 127.0.0.1 only; credentials are passed via a 0600 config file
  that is deleted as soon as the engine is up.
- Engine log: `~/.cache/xmrtui/wallet-rpc.log`.
