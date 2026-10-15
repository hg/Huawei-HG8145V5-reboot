# ONT reconnect

> [!CAUTION]
> DANGER: vibecoded

Reboots a Huawei HG8145V5 ONT, waits for the network to drop and come back,
then notifies. No browser needed.

Two implementations share the same protocol:

- `reconnect.sh` — original bash/curl version.
- Go port (`main.go`, `config.go`, `ont.go`, `publicip.go`, `ping.go`,
  `run.go`, `notify.go`, `logger.go`) — pure stdlib, zero third-party deps.

## Requirements

Shell: `bash`, `curl`, `ping`, `notify-send`, `base64`, `grep`.

Go: `go >= 1.23`, `ping`, optional `notify-send`. No Go modules beyond stdlib.

## Quick start

```bash
cp .env.example .env   # then set ONT_PASS in .env

# shell
./reconnect.sh

# go (same .env, same behavior)
go run . 
# or
go build -o reconnect-go . && ./reconnect-go
```

`ONT_URL` and `ONT_USER` can also be overridden in `.env`; `PING_TIMEOUT`
(seconds, default 600) can go there too. The Go port additionally honors
`DROP_TIMEOUT` (default 90), `REBOOT_MAX_TIME` (default 20),
`ONT_PING_HOST` (default 1.1.1.1), `ONT_PACE` (default 2),
`ONT_PING_INTERVAL` (default 2) and `ONT_DOWN_SLEEP` (default 5) — each as
plain seconds or a Go duration (`90s`, `2m`). CLI flags (`--url`, `--user`,
`--pass`, `--ping-timeout`, `--drop-timeout`, `--reboot-max-time`,
`--ping-host`, `--env-file`) override env/`.env`.

## How it works

1. Fetch a one-time login token, log in as root.
2. Scrape the reboot token (`onttoken`) from the device-management page.
3. POST the reboot command — the ONT hangs the response, so a reset
   (`curl: (56)`) or a timeout (capped with `--max-time`, `REBOOT_MAX_TIME`,
   default 20s) is the expected success signal.
4. Best-effort wait for pings to drop (the drop may already be over if the
   POST blocked — no longer fatal), then ping until back and compare IPs.
5. `notify-send reconnected`.

The ONT rate-limits rapid requests, so the script pauses ~2s between calls.
Protocol details: see [FINDINGS.md](FINDINGS.md).

## Testing (Go, mocked — no real hardware)

All Go tests stub the ONT with `httptest` servers, DNS with injected
lookups, and ping with scripted sequences. They never dial `192.168.100.1`,
`1.1.1.1`, or the public-IP resolvers:

```bash
go vet ./...
go test ./... -v
go test -coverprofile=/tmp/cover.out ./... && go tool cover -func=/tmp/cover.out
```

## Files

- `reconnect.sh` — original shell implementation
- `main.go`, `config.go`, `ont.go`, `publicip.go`, `ping.go`, `run.go`,
  `notify.go`, `logger.go` — Go port (stdlib only)
- `*_test.go` — mocked test coverage (no hardware)
- `.env.example` — config template (real secrets go in `.env`, which is gitignored)
- `FINDINGS.md` — reverse-engineered login/session/reboot protocol
