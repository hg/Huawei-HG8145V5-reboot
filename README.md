# ONT reconnect

Reboots a Huawei HG8145V5 ONT over plain curl, waits for the network to drop
and come back, then notifies. No browser needed.

## Requirements

`bash`, `curl`, `ping`, `notify-send`, `base64`, `grep`.

## Quick start

```bash
cp .env.example .env   # then set ONT_PASS in .env
./reconnect.sh
```

`ONT_URL` and `ONT_USER` can also be overridden in `.env`; `PING_TIMEOUT`
(seconds, default 600) can go there too.

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

## Files

- `reconnect.sh` — the whole thing
- `.env.example` — config template (real secrets go in `.env`, which is gitignored)
- `FINDINGS.md` — reverse-engineered login/session/reboot protocol
