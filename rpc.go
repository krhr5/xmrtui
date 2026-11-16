package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RPC is a minimal Monero JSON-RPC client. It speaks HTTP digest auth
// (MD5, qop=auth), which is what monero-wallet-rpc requires with --rpc-login.
type RPC struct {
	endpoint   string
	user, pass string
	hc         *http.Client

	mu     sync.Mutex
	realm  string
	nonce  string
	opaque string
	nc     uint32
}

func NewRPC(endpoint, user, pass string) *RPC {
	return &RPC{endpoint: endpoint, user: user, pass: pass, hc: &http.Client{}}
}

// daemonEndpoint turns "host:port" (or a full URL) into a /json_rpc URL.
func daemonEndpoint(addr string) string {
	addr = strings.TrimSpace(addr)
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/") + "/json_rpc"
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

func (r *RPC) Call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "0", "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	resp, err := r.post(ctx, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&env); err != nil {
		return fmt.Errorf("%s: bad response: %w", method, err)
	}
	if env.Error != nil {
		return env.Error
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func (r *RPC) post(ctx context.Context, body []byte) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if r.user != "" {
			if h := r.authHeader(req.URL); h != "" {
				req.Header.Set("Authorization", h)
			}
		}
		resp, err := r.hc.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && r.user != "" && attempt == 0 {
			ok := r.parseChallenge(resp.Header.Values("WWW-Authenticate"))
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if !ok {
				return nil, errors.New("unsupported auth challenge")
			}
			continue
		}
		return resp, nil
	}
	return nil, errors.New("authentication failed")
}

func (r *RPC) parseChallenge(values []string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if !strings.HasPrefix(strings.ToLower(v), "digest ") {
			continue
		}
		p := parseAuthParams(v[7:])
		if alg := strings.ToUpper(p["algorithm"]); alg != "" && alg != "MD5" {
			continue
		}
		r.realm, r.nonce, r.opaque, r.nc = p["realm"], p["nonce"], p["opaque"], 0
		return r.nonce != ""
	}
	return false
}

func (r *RPC) authHeader(u *url.URL) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nonce == "" {
		return ""
	}
	r.nc++
	nc := fmt.Sprintf("%08x", r.nc)
	cb := make([]byte, 8)
	rand.Read(cb)
	cnonce := hex.EncodeToString(cb)
	uri := u.RequestURI()
	ha1 := md5hex(r.user + ":" + r.realm + ":" + r.pass)
	ha2 := md5hex("POST:" + uri)
	resp := md5hex(ha1 + ":" + r.nonce + ":" + nc + ":" + cnonce + ":auth:" + ha2)
	h := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=MD5, qop=auth, nc=%s, cnonce="%s", response="%s"`,
		r.user, r.realm, r.nonce, uri, nc, cnonce, resp)
	if r.opaque != "" {
		h += fmt.Sprintf(`, opaque="%s"`, r.opaque)
	}
	return h
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// parseAuthParams parses `k="v", k2=v2` respecting quoted commas.
func parseAuthParams(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ,")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var val string
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				val, s = s[1:], ""
			} else {
				val, s = s[1:end+1], s[end+2:]
			}
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				val, s = s, ""
			} else {
				val, s = s[:end], s[end:]
			}
		}
		out[key] = strings.TrimSpace(val)
	}
	return out
}

func ctxT(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// ---- Wallet / daemon types ----

type Balance struct {
	Balance        uint64 `json:"balance"`
	Unlocked       uint64 `json:"unlocked_balance"`
	BlocksToUnlock uint64 `json:"blocks_to_unlock"`
}

type SubAddr struct {
	Address string `json:"address"`
	Index   uint32 `json:"address_index"`
	Label   string `json:"label"`
	Used    bool   `json:"used"`
}

type Transfer struct {
	Address       string `json:"address"`
	Amount        uint64 `json:"amount"`
	Confirmations uint64 `json:"confirmations"`
	Fee           uint64 `json:"fee"`
	Height        uint64 `json:"height"`
	Note          string `json:"note"`
	PaymentID     string `json:"payment_id"`
	Timestamp     int64  `json:"timestamp"`
	TxID          string `json:"txid"`
	Type          string `json:"type"`
	Locked        bool   `json:"locked"`
	DoubleSpend   bool   `json:"double_spend_seen"`
	SubaddrIndex  struct {
		Major uint32 `json:"major"`
		Minor uint32 `json:"minor"`
	} `json:"subaddr_index"`
	Destinations []struct {
		Address string `json:"address"`
		Amount  uint64 `json:"amount"`
	} `json:"destinations"`
}

type DaemonInfo struct {
	Height       uint64 `json:"height"`
	TargetHeight uint64 `json:"target_height"`
	Status       string `json:"status"`
	Synchronized bool   `json:"synchronized"`
	Version      string `json:"version"`
	Nettype      string `json:"nettype"`
	Outgoing     uint64 `json:"outgoing_connections_count"`
	Incoming     uint64 `json:"incoming_connections_count"`
	TxPoolSize   uint64 `json:"tx_pool_size"`
	Restricted   bool   `json:"restricted"`
	Untrusted    bool   `json:"untrusted"`
}

type PreparedTx struct {
	Amount     uint64 `json:"amount"`
	Fee        uint64 `json:"fee"`
	TxHash     string `json:"tx_hash"`
	TxKey      string `json:"tx_key"`
	TxMetadata string `json:"tx_metadata"`
}
