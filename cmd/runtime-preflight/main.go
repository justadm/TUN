package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"tun/internal/tun"
)

func main() {
	tunName := flag.String("tun-name", "", "desired TUN interface name (linux only)")
	tunMTU := flag.Int("tun-mtu", 0, "optional TUN MTU (0 keeps system default)")
	tunSkipUp := flag.Bool("tun-skip-up", false, "do not set TUN interface UP on open")
	tunAddresses := flag.String("tun-addresses", "", "optional comma-separated interface CIDRs (linux only)")
	tunRoutes := flag.String("tun-routes", "", "optional comma-separated route CIDRs or 'default' (linux only)")
	tunConfigMode := flag.String("tun-config-mode", "replace", "TUN address/route apply mode: replace|add")
	tunCleanupOnClose := flag.Bool("tun-cleanup-on-close", false, "remove configured addresses/routes on device close")
	flag.Parse()

	opts := tun.OpenOptions{
		Name:           *tunName,
		MTU:            *tunMTU,
		SkipUp:         *tunSkipUp,
		Addresses:      parseCSVList(*tunAddresses),
		Routes:         parseCSVList(*tunRoutes),
		ConfigMode:     *tunConfigMode,
		CleanupOnClose: *tunCleanupOnClose,
	}

	report, err := tun.BuildPreflightReport(opts)
	raw, mErr := json.MarshalIndent(report, "", "  ")
	if mErr != nil {
		fmt.Fprintf(os.Stderr, "preflight marshal failed: %v\n", mErr)
		os.Exit(1)
	}
	fmt.Println(string(raw))
	if err != nil {
		os.Exit(1)
	}
}

func parseCSVList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
