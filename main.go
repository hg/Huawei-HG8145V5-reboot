package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if err := realMain(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] FATAL: %s\n", time.Now().Format("15:04:05"), err)
		os.Exit(1)
	}
}

func realMain(args []string) error {
	fs := flag.NewFlagSet("reconnect", flag.ContinueOnError)
	var (
		urlFlag      = fs.String("url", "", "ONT base URL (overrides ONT_URL)")
		userFlag     = fs.String("user", "", "ONT user (overrides ONT_USER)")
		passFlag     = fs.String("pass", "", "ONT password (overrides ONT_PASS)")
		pingTOFlag   = fs.String("ping-timeout", "", "ping timeout secs or duration (overrides PING_TIMEOUT)")
		dropTOFlag   = fs.String("drop-timeout", "", "drop-wait secs or duration (overrides DROP_TIMEOUT)")
		rebootFlag   = fs.String("reboot-max-time", "", "reboot POST cap secs or duration (overrides REBOOT_MAX_TIME)")
		pingHostFlag = fs.String("ping-host", "", "host to ping (overrides ONT_PING_HOST, default 1.1.1.1)")
		envFileFlag  = fs.String("env-file", "", ".env file to load (default: ./.env plus executable dir)")
	)
	// Parse but don't print usage on error twice; let flag handle it.
	if err := fs.Parse(args); err != nil {
		return err
	}

	overrides := map[string]string{}
	if strings.TrimSpace(*urlFlag) != "" {
		overrides["ONT_URL"] = strings.TrimSpace(*urlFlag)
	}
	if *userFlag != "" {
		overrides["ONT_USER"] = *userFlag
	}
	if *passFlag != "" {
		overrides["ONT_PASS"] = *passFlag
	}
	if strings.TrimSpace(*pingTOFlag) != "" {
		overrides["PING_TIMEOUT"] = strings.TrimSpace(*pingTOFlag)
	}
	if strings.TrimSpace(*dropTOFlag) != "" {
		overrides["DROP_TIMEOUT"] = strings.TrimSpace(*dropTOFlag)
	}
	if strings.TrimSpace(*rebootFlag) != "" {
		overrides["REBOOT_MAX_TIME"] = strings.TrimSpace(*rebootFlag)
	}
	if strings.TrimSpace(*pingHostFlag) != "" {
		overrides["ONT_PING_HOST"] = strings.TrimSpace(*pingHostFlag)
	}

	var envFiles []string
	if *envFileFlag != "" {
		envFiles = []string{*envFileFlag}
	} else {
		envFiles = DefaultEnvFiles()
	}

	cfg, err := LoadConfig(overrides, envFiles)
	if err != nil {
		return err
	}

	client, err := NewONTClient(cfg.URL, cfg.RebootMax)
	if err != nil {
		return err
	}

	logger := NewLogger(os.Stdout)
	resolver := NewPublicIPResolver()
	deps := RunDeps{
		ONT:      client,
		PublicIP: func(ctx context.Context) string { return resolver.PublicIP(ctx) },
		Pinger:   ExecPinger{},
		Notify:   NewNotifier(os.Stdout, nil, nil),
		Sleep:    time.Sleep,
		Log:      logger,
	}

	_, err = Run(context.Background(), cfg, deps)
	return err
}
