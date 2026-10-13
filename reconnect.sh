#!/usr/bin/env bash
# Reboot a Huawei HG8145V5 ONT with curl only.
# Deps: curl, ping, drill, notify-send, base64, grep. No browser needed.
#
# Flow:
#   1. print public IP before reset
#   2. POST .../GetRandCount.asp -> one-time login token
#   3. POST .../login.cgi (UserName, base64 PassWord, Language, token)
#   4. GET .../ontmngt.asp -> extract hidden "onttoken" field value
#   5. POST set.cgi?...ResetBoard... with the onttoken -> reboot
#      (POST is capped with --max-time: the ONT hangs the response, so a
#      timeout/reset here is the success signal, not a failure)
#   6. best-effort wait for ping to DROP (missed if the POST blocked longer
#      than the outage), sleep 5s if a drop was seen
#   7. ping until success -> print public IP after reset -> notify-send reconnected
#
# NOTE: the ONT drops rapid requests — the small sleeps between calls are required.
#
# Setup: cp .env.example .env, then set ONT_PASS in .env
#        (ONT_URL/ONT_USER can also be overridden there).
# Usage: ./reconnect.sh
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null && pwd)
if [[ -f "$SCRIPT_DIR/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  . "$SCRIPT_DIR/.env"
  set +a
fi

URL="${ONT_URL:-http://192.168.100.1}"
USER="${ONT_USER:-root}"
: "${ONT_PASS:?ONT_PASS is not set (copy .env.example to .env and fill it in)}"
PASS="$ONT_PASS"
PING_TIMEOUT="${PING_TIMEOUT:-600}"
DROP_TIMEOUT=90
# The ONT hangs the reboot POST instead of answering it, so curl would sit
# there past the whole outage. Cap it so we get control back in time to see
# the drop (or the recovery).
REBOOT_MAX_TIME="${REBOOT_MAX_TIME:-20}"

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
die() {
  log "FATAL: $*"
  exit 1
}
dns_ip() { drill -Q -4 "$@" 2>/dev/null | grep -Eo '[0-9]{1,3}(\.[0-9]{1,3}){3}' | head -n 1 || true; }
public_ip() {
  local ip
  ip=$(dns_ip A myip.opendns.com @resolver1.opendns.com)
  [[ -n $ip ]] && {
    printf '%s' "$ip"
    return 0
  }
  ip=$(dns_ip TXT o-o.myaddr.l.google.com @ns1.google.com)
  [[ -n $ip ]] && {
    printf '%s' "$ip"
    return 0
  }
  ip=$(dns_ip TXT whoami.cloudflare.com @ns3.cloudflare.com)
  [[ -n $ip ]] && {
    printf '%s' "$ip"
    return 0
  }
  echo "unknown"
}

JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

BEFORE_IP=$(public_ip || true)
log "public IP before reset: $BEFORE_IP"

log "GET login page"
curl -fsSL -c "$JAR" -b "$JAR" "$URL/" -o /dev/null
sleep 2

log "fetch login token"
TOKEN=$(curl -fsSL -b "$JAR" -X POST "$URL/asp/GetRandCount.asp" | tr -d '\r\n﻿')
[[ $TOKEN =~ ^[0-9a-f]{32,}$ ]] || die "bad login token: '$TOKEN'"
sleep 2

log "login as $USER"
PASS_B64=$(printf '%s' "$PASS" | base64)
LOGIN_RESP=$(curl -fsSL -c "$JAR" -b "$JAR" -X POST "$URL/login.cgi" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode "UserName=$USER" \
  --data-urlencode "PassWord=$PASS_B64" \
  --data-urlencode 'Language=english' \
  --data-urlencode "x.X_HW_Token=$TOKEN")
grep -q "pageName = 'index.asp'" <<<"$LOGIN_RESP" || die "login failed: $(head -c 200 <<<"$LOGIN_RESP")"
log "login OK"
sleep 2

log "fetch reboot token (onttoken)"
MGMT=$(curl -fsSL -c "$JAR" -b "$JAR" "$URL/html/ssmp/accoutcfg/ontmngt.asp")
ONTTOKEN=$(grep -o -E 'name="onttoken"[^>]*' <<<"$MGMT" | grep -o -E 'value="[0-9a-f]+"' | cut -d'"' -f2 | head -n 1)
[[ $ONTTOKEN =~ ^[0-9a-f]{32,}$ ]] || die "onttoken not found (session dead?)"
log "onttoken=${ONTTOKEN:0:12}..."
sleep 2

log "POST reboot (a timeout/reset here means the ONT is going down)"
RC=0
curl -fsSL --connect-timeout 10 --max-time "$REBOOT_MAX_TIME" -b "$JAR" -X POST \
  "$URL/html/ssmp/accoutcfg/set.cgi?x=InternetGatewayDevice.X_HW_DEBUG.SMP.DM.ResetBoard&RequestFile=html/ssmp/accoutcfg/ontmngt.asp" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode "x.X_HW_Token=$ONTTOKEN" -o /dev/null || RC=$?
if ((RC == 0)); then
  log "reboot posted (clean response)"
elif ((RC == 56)); then
  log "connection reset by peer — ONT is going down"
elif ((RC == 28)); then
  log "POST timed out after ${REBOOT_MAX_TIME}s — ONT is going down"
else
  die "reboot POST failed (curl rc=$RC)"
fi

log "waiting for network to drop (best-effort: may already be back if the POST blocked)"
DROPPED=0
START=$(date +%s)
while ping -c1 -W2 1.1.1.1 >/dev/null 2>&1; do
  if (($(date +%s) - START > DROP_TIMEOUT)); then
    log "no drop observed within ${DROP_TIMEOUT}s — continuing anyway (outage may have been missed)"
    break
  fi
  sleep 2
done
if ping -c1 -W2 1.1.1.1 >/dev/null 2>&1; then
  log "network still up, skipping down-sleep"
else
  DROPPED=1
  log "network down, sleeping 5s"
  sleep 5
fi

log "pinging 1.1.1.1 until success (timeout ${PING_TIMEOUT}s)"
START=$(date +%s)
while ! ping -c1 -W2 1.1.1.1 >/dev/null 2>&1; do
  ELAPSED=$(($(date +%s) - START))
  ((ELAPSED > PING_TIMEOUT)) && die "1.1.1.1 unreachable for ${PING_TIMEOUT}s"
  ((ELAPSED > 0 && ELAPSED % 20 == 0)) && log "still down after ${ELAPSED}s..."
  sleep 2
done
ELAPSED=$(($(date +%s) - START))
if ((DROPPED == 1)); then
  log "ping 1.1.1.1 OK after ${ELAPSED}s down (drop was observed)"
else
  log "ping 1.1.1.1 OK (no drop observed — outage was missed or instant)"
fi

AFTER_IP=$(public_ip || true)
log "public IP after reset: $AFTER_IP"
if [[ $BEFORE_IP != "$AFTER_IP" ]]; then
  log "IP changed: $BEFORE_IP -> $AFTER_IP"
else
  log "IP unchanged: $AFTER_IP"
fi

if command -v notify-send >/dev/null 2>&1 && notify-send "reconnected" "ONT reboot done — 1.1.1.1 back after ${ELAPSED}s down ($BEFORE_IP -> $AFTER_IP)"; then
  log "notification sent"
else
  echo "reconnected"
fi
log "done"
