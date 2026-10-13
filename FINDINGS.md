# ONT findings — Huawei HG8145V5

Reverse-engineered by watching the real web UI (Playwright request capture) and
replaying it with curl. `reconnect.sh` implements exactly this flow.

## Login page (`/`)

- One page, several panels. **Four elements share `id="loginbutton"`** — three sit
  in hidden panels. Automation must pick the *visible* one, never `.first()`.
- Visible fields: `#txt_Username`, `#txt_Password`.
  `#txt_Username2`/`#txt_Password2` are a hidden variant used only when the
  firmware `CfgMode` is `ORANGEMT2`.
- The captcha (`#VerificationCode`) is `display:none` on this unit (only required
  in `TOT`/`THAILANDNT2` modes). The password-change and WiFi-SSID panels are
  hidden too.
- `LoginSubmit()` validates non-empty fields, then:
  1. `POST /asp/GetRandCount.asp` (empty body, synchronous) -> hex token.
     The body is **BOM-prefixed** (`\xef\xbb\xbf` must be stripped).
  2. Form-POSTs `/login.cgi` with `UserName`, `PassWord=base64(password)`,
     `Language=english`, `x.X_HW_Token=<that token>`.

## Session handling

- The server **never sends `Set-Cookie`**; the cookie jar stays empty. The session
  is tracked by **client IP**. Any hardcoded `sid=` cookie is unnecessary.
- `login.cgi` answers with a `Waiting...` page: `top.location.replace('index.asp')`
  means success. `pageName = '/'` means failure or an expired/kicked session —
  every authed URL returns that same page when unauthenticated.
- The ONT has a **single web session**: a new login kicks the previous one.
- **Pacing matters**: leave ~2s between requests. Hammering it back-to-back makes
  every request answer with the Waiting `/` page.

## Post-login UI (frames: `index.asp`, `CustomApp/mainpage.asp`, `refresh.asp`, …)

- `#RestartIcon` lives on `mainpage.asp`. Clicking it loads
  `html/ssmp/accoutcfg/ontmngt.asp` into another frame.
- That page has a Restart button (`#btnReboot`). Its click handler
  pops a native **confirm** asking whether to restart despite losing unsaved
  data, and on accept form-POSTs
  `set.cgi?x=InternetGatewayDevice.X_HW_DEBUG.SMP.DM.ResetBoard&RequestFile=html/ssmp/accoutcfg/ontmngt.asp`
  with `x.X_HW_Token=<value of the hidden #onttoken field>`.
- Calling a bare top-level reboot function from the console does nothing
  observable: it needs the
  management-frame context **and** an accepted confirm. Browser automation that
  auto-dismisses dialogs silently cancels the reboot.

## Tokens (both change constantly — nothing is hardcoded)

- `GetRandCount.asp` token: one-time, consumed by the next state-changing POST
  (the login). A fresh one must not be reused for the reboot.
- `onttoken`: page-embedded reboot token, scraped fresh from `ontmngt.asp`
  (`name="onttoken"`, 64 hex chars) per session.

## Reboot signature

- The `set.cgi` POST usually ends in `curl: (56) connection reset` — the ONT's
  HTTP server dies with it — or in a timeout once `--max-time` is hit, since
  the ONT hangs the response. Either **is** the success signal, so the POST
  is capped (`REBOOT_MAX_TIME`, default 20s) to get control back quickly.
- Ping test: a drop-then-recovery means a real reboot. But the drop window can
  be missed if the POST blocked, so the drop wait is best-effort, not fatal;
  the before/after public-IP comparison is the backstop.

## Secrets

- The password lives only in `.env` (gitignored) and is sent as
  `base64($ONT_PASS)` computed at runtime. Nothing secret is committed.
