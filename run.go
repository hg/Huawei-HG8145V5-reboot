package main

import (
	"context"
	"fmt"
	"time"
)

// ONT mirrors *ONTClient for test fakes.
type ONT interface {
	GetLoginPage(ctx context.Context) error
	FetchLoginToken(ctx context.Context) (string, error)
	Login(ctx context.Context, user, pass, token string) error
	FetchOntToken(ctx context.Context) (string, error)
	Reboot(ctx context.Context, ontToken string) (RebootOutcome, error)
}

// RunDeps bundles side effects so Run is fully testable with a mock
// HTTP server (ONT), stub DNS (PublicIP) and stub ping (Pinger).
type RunDeps struct {
	ONT      ONT
	PublicIP func(ctx context.Context) string
	Pinger   Pinger
	Notify   Notifier
	Sleep    func(time.Duration)
	Log      *Logger
}

// RunResult summarizes a successful reconnect.
type RunResult struct {
	BeforeIP string
	AfterIP  string
	Dropped  bool
	DownFor  time.Duration
	Reboot   RebootOutcome
}

// Run implements reconnect.sh end to end.
func Run(ctx context.Context, cfg Config, d RunDeps) (RunResult, error) {
	var res RunResult
	log := d.Log
	if log == nil {
		log = NewLogger(discardWriter{})
	}
	sleep := d.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	publicIP := d.PublicIP
	if publicIP == nil {
		publicIP = func(context.Context) string { return unknownIP }
	}
	pace := func() {
		if cfg.Pace <= 0 {
			return
		}
		select {
		case <-ctx.Done():
		default:
		}
		sleep(cfg.Pace)
		select {
		case <-ctx.Done():
		default:
		}
	}

	if err := ctx.Err(); err != nil {
		return res, err
	}

	res.BeforeIP = publicIP(ctx)
	log.Logf("public IP before reset: %s", res.BeforeIP)

	log.Log("GET login page")
	if err := d.ONT.GetLoginPage(ctx); err != nil {
		return res, err
	}
	pace()

	log.Log("fetch login token")
	loginToken, err := d.ONT.FetchLoginToken(ctx)
	if err != nil {
		return res, err
	}
	pace()

	log.Logf("login as %s", cfg.User)
	if err := d.ONT.Login(ctx, cfg.User, cfg.Pass, loginToken); err != nil {
		return res, err
	}
	log.Log("login OK")
	pace()

	log.Log("fetch reboot token (onttoken)")
	ontToken, err := d.ONT.FetchOntToken(ctx)
	if err != nil {
		return res, err
	}
	prefix := ontToken
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	log.Logf("onttoken=%s...", prefix)
	pace()

	log.Log("POST reboot (a timeout/reset here means the ONT is going down)")
	outcome, err := d.ONT.Reboot(ctx, ontToken)
	if err != nil {
		return res, err
	}
	res.Reboot = outcome
	switch outcome {
	case RebootClean:
		log.Log("reboot posted (clean response)")
	case RebootReset:
		log.Log("connection reset by peer — ONT is going down")
	case RebootTimeout:
		log.Logf("POST timed out after %s — ONT is going down", formatSecs(cfg.RebootMax))
	default:
		log.Logf("reboot posted (%s)", string(outcome))
	}

	log.Log("waiting for network to drop (best-effort: may already be back if the POST blocked)")
	dropped := waitForDrop(ctx, cfg, d.Pinger, sleep, log)
	res.Dropped = dropped
	if !dropped {
		// waitForDrop already logged the reason.
	} else {
		log.Logf("network down, sleeping %s", formatSecs(cfg.DownSleep))
		sleep(cfg.DownSleep)
	}

	log.Logf("pinging %s until success (timeout %s)", cfg.PingHost, formatSecs(cfg.PingTimeout))
	downFor, err := waitForUp(ctx, cfg, d.Pinger, sleep, log)
	if err != nil {
		return res, err
	}
	res.DownFor = downFor
	if dropped {
		log.Logf("ping %s OK after %s down (drop was observed)", cfg.PingHost, formatSecs(downFor))
	} else {
		log.Logf("ping %s OK (no drop observed — outage was missed or instant)", cfg.PingHost)
	}

	res.AfterIP = publicIP(ctx)
	log.Logf("public IP after reset: %s", res.AfterIP)
	if res.BeforeIP != res.AfterIP {
		log.Logf("IP changed: %s -> %s", res.BeforeIP, res.AfterIP)
	} else {
		log.Logf("IP unchanged: %s", res.AfterIP)
	}

	notify := d.Notify
	if notify != nil {
		body := fmt.Sprintf("ONT reboot done — %s back after %s down (%s -> %s)",
			cfg.PingHost, formatSecs(downFor), res.BeforeIP, res.AfterIP)
		_ = notify("reconnected", body)
		log.Log("notification sent")
	}
	log.Log("done")
	return res, nil
}

// waitForDrop mirrors the shell's best-effort drop wait: loop while ping
// succeeds until DropTimeout, then do one final probe to decide dropped.
func waitForDrop(ctx context.Context, cfg Config, p Pinger, sleep func(time.Duration), log *Logger) bool {
	if p == nil {
		return false
	}
	start := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return false
		}
		if !p.Ping(ctx, cfg.PingHost) {
			break // down observed inside the wait loop
		}
		if time.Since(start) > cfg.DropTimeout {
			log.Logf("no drop observed within %s — continuing anyway (outage may have been missed)",
				formatSecs(cfg.DropTimeout))
			break
		}
		sleep(cfg.PingInterval)
	}
	// Final probe like the shell's `if ping ... else DROPPED=1`.
	if p.Ping(ctx, cfg.PingHost) {
		log.Log("network still up, skipping down-sleep")
		return false
	}
	return true
}

// waitForUp pings until success or PingTimeout, logging every 20s.
func waitForUp(ctx context.Context, cfg Config, p Pinger, sleep func(time.Duration), log *Logger) (time.Duration, error) {
	return waitForUpEvery(ctx, cfg, p, sleep, log, DefaultStillDownEv)
}

func waitForUpEvery(ctx context.Context, cfg Config, p Pinger, sleep func(time.Duration), log *Logger, logEvery time.Duration) (time.Duration, error) {
	if p == nil {
		return 0, fmt.Errorf("%s unreachable: no pinger", cfg.PingHost)
	}
	if logEvery <= 0 {
		logEvery = DefaultStillDownEv
	}
	start := time.Now()
	nextLog := logEvery
	for {
		if err := ctx.Err(); err != nil {
			return time.Since(start), err
		}
		if p.Ping(ctx, cfg.PingHost) {
			return time.Since(start), nil
		}
		elapsed := time.Since(start)
		if elapsed > cfg.PingTimeout {
			return elapsed, fmt.Errorf("%s unreachable for %s", cfg.PingHost, formatSecs(cfg.PingTimeout))
		}
		if elapsed >= nextLog {
			log.Logf("still down after %s...", formatSecs(elapsed))
			nextLog += logEvery
		}
		sleep(cfg.PingInterval)
	}
}

func formatSecs(d time.Duration) string {
	secs := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%ds", secs)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
