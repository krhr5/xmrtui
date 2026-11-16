package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

const defaultDaemon = "xmr-node.cakewallet.com:18081"

func main() {
	walletFlag := flag.String("wallet", os.Getenv("XMRTUI_WALLET"), "path to wallet file (without .keys)")
	daemonFlag := flag.String("daemon", envOr("XMRTUI_DAEMON", defaultDaemon), "daemon host:port")
	rpcBin := flag.String("rpc-bin", "monero-wallet-rpc", "monero-wallet-rpc binary")
	flag.Parse()

	if err := run(*walletFlag, *daemonFlag, *rpcBin); err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Render("✖ "+err.Error()))
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run(walletPath, daemon, rpcBin string) error {
	walletPath, err := resolveWallet(walletPath)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath(rpcBin); err != nil {
		return fmt.Errorf("%s not found (install with: omarchy pkg add monero)", rpcBin)
	}
	walletDir, walletName := filepath.Dir(walletPath), filepath.Base(walletPath)

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().MarginLeft(2).Render(pixelText("XMR", 1, gradient(cOrangeHi, cOrangeLo), cShadow)))
	fmt.Println()
	fmt.Printf("  %s %s\n", dimStyle.Render("wallet"), walletPath)
	fmt.Printf("  %s %s\n\n", dimStyle.Render("node  "), daemon)
	fmt.Print(accentStyle.Render("  password ▸ "))
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}
	defer func() {
		for i := range pw {
			pw[i] = 0
		}
	}()

	// Private temp dir holds a config file with random RPC credentials, so
	// they never appear on the process command line.
	tmp, err := os.MkdirTemp("", "xmrtui-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	user, pass := randHex(8), randHex(24)
	cfg := filepath.Join(tmp, "rpc.conf")
	if err := os.WriteFile(cfg, []byte("rpc-login="+user+":"+pass+"\n"), 0o600); err != nil {
		return err
	}

	port, err := freePort()
	if err != nil {
		return err
	}
	cacheDir, _ := os.UserCacheDir()
	logDir := filepath.Join(cacheDir, "xmrtui")
	os.MkdirAll(logDir, 0o700)
	logPath := filepath.Join(logDir, "wallet-rpc.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()

	cmd := exec.Command(rpcBin,
		"--config-file", cfg,
		"--wallet-dir", walletDir,
		"--daemon-address", daemon,
		"--untrusted-daemon",
		"--rpc-bind-ip", "127.0.0.1",
		"--rpc-bind-port", fmt.Sprint(port),
		"--non-interactive",
		"--log-file", logPath,
		"--log-level", "0",
	)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
	fmt.Println(dimStyle.Render("  starting wallet engine…"))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", rpcBin, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	wallet := NewRPC(fmt.Sprintf("http://127.0.0.1:%d/json_rpc", port), user, pass)
	shutdown := func() {
		ctx, cancel := ctxT(10 * time.Second)
		wallet.Call(ctx, "stop_wallet", nil, nil) // saves and exits
		cancel()
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
			}
		}
	}

	// Ctrl-C before the TUI is up should still shut the engine down cleanly.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	ready := false
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-exited:
			return fmt.Errorf("wallet engine exited early (%v) — see %s", err, logPath)
		case <-sig:
			shutdown()
			return errors.New("cancelled")
		case <-time.After(300 * time.Millisecond):
		}
		ctx, cancel := ctxT(2 * time.Second)
		err := wallet.Call(ctx, "get_version", nil, nil)
		cancel()
		if err == nil {
			ready = true
			break
		}
	}
	os.Remove(cfg)
	if !ready {
		shutdown()
		return fmt.Errorf("wallet engine did not start — see %s", logPath)
	}

	fmt.Println(dimStyle.Render("  opening wallet…"))
	ctx, cancel := ctxT(3 * time.Minute)
	err = wallet.Call(ctx, "open_wallet", map[string]any{"filename": walletName, "password": string(pw)}, nil)
	cancel()
	if err != nil {
		shutdown()
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "password") || strings.Contains(msg, "Failed to open wallet") {
			msg += " (wrong password, or the wallet is open in monero-wallet-cli)"
		}
		return errors.New(msg)
	}
	ctx, cancel = ctxT(5 * time.Second)
	wallet.Call(ctx, "auto_refresh", map[string]any{"enable": true, "period": 10}, nil)
	cancel()

	signal.Stop(sig)
	p := tea.NewProgram(newModel(wallet, daemon, walletName), tea.WithAltScreen())
	_, runErr := p.Run()

	fmt.Println(dimStyle.Render("  saving wallet and shutting down…"))
	shutdown()
	return runErr
}

func resolveWallet(p string) (string, error) {
	home, _ := os.UserHomeDir()
	if p == "" {
		var found []string
		for _, pat := range []string{
			filepath.Join(home, "*.keys"),
			filepath.Join(home, "Monero", "wallets", "*", "*.keys"),
		} {
			m, _ := filepath.Glob(pat)
			found = append(found, m...)
		}
		switch len(found) {
		case 0:
			return "", errors.New("no wallet found — pass -wallet /path/to/wallet")
		case 1:
			p = found[0]
		default:
			return "", fmt.Errorf("several wallets found, pick one with -wallet:\n  %s", strings.Join(found, "\n  "))
		}
	}
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	p = strings.TrimSuffix(p, ".keys")
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs + ".keys"); err != nil {
		return "", fmt.Errorf("wallet keys file not found: %s.keys", abs)
	}
	return abs, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
