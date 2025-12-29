package cli

import (
	"errors"
	"flag"
	"os"
	"strings"
)

type rpcFlags struct {
	rpcURL  string
	rpcUser string
	rpcPass string
}

func (r *rpcFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&r.rpcURL, "rpc-url", "", "junocashd RPC URL")
	fs.StringVar(&r.rpcUser, "rpc-user", "", "junocashd RPC username")
	fs.StringVar(&r.rpcPass, "rpc-pass", "", "junocashd RPC password")
}

func (r *rpcFlags) resolved() (string, string, string, error) {
	url := strings.TrimSpace(r.rpcURL)
	user := strings.TrimSpace(r.rpcUser)
	pass := strings.TrimSpace(r.rpcPass)
	if url == "" {
		url = strings.TrimSpace(os.Getenv("JUNO_RPC_URL"))
	}
	if user == "" {
		user = strings.TrimSpace(os.Getenv("JUNO_RPC_USER"))
	}
	if pass == "" {
		pass = strings.TrimSpace(os.Getenv("JUNO_RPC_PASS"))
	}
	if url == "" {
		return "", "", "", errors.New("rpc url required (set --rpc-url or JUNO_RPC_URL)")
	}
	if user == "" || pass == "" {
		return "", "", "", errors.New("rpc user/pass required (set --rpc-user/--rpc-pass or JUNO_RPC_USER/JUNO_RPC_PASS)")
	}
	return url, user, pass, nil
}

type servicesFlags struct {
	scanURL      string
	broadcastURL string
}

func (s *servicesFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&s.scanURL, "scan-url", "", "juno-scan base URL")
	fs.StringVar(&s.broadcastURL, "broadcast-url", "", "juno-broadcast base URL")
}

func (s *servicesFlags) resolvedScanURL() string {
	if v := strings.TrimSpace(s.scanURL); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("JUNO_SCAN_URL"))
}

func (s *servicesFlags) resolvedBroadcastURL() string {
	if v := strings.TrimSpace(s.broadcastURL); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("JUNO_BROADCAST_URL"))
}

