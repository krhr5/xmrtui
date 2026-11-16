package main

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	qrcode "github.com/skip2/go-qrcode"
)

// ---- palette ----

const (
	cOrange   = "#FF6600"
	cOrangeHi = "#FFA24D"
	cOrangeLo = "#E04E00"
	cShadow   = "#4A1E00"
	cFrame    = "#7A3300"
	cGrey     = "#4C4C4C"
	cText     = "#E8E8E8"
	cDim      = "#8A8A8A"
	cGreen    = "#5FD787"
	cRed      = "#FF5F5F"
	cYellow   = "#FFD75F"
)

var (
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(cOrange)).Bold(true)
	textStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(cText))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(cDim))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(cRed)).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(cGreen))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(cYellow))
	tagStyle    = lipgloss.NewStyle().Background(lipgloss.Color(cOrange)).Foreground(lipgloss.Color("#000000")).Bold(true)
	tabOn       = lipgloss.NewStyle().Background(lipgloss.Color(cOrange)).Foreground(lipgloss.Color("#000000")).Bold(true).Padding(0, 1)
	tabOff      = lipgloss.NewStyle().Foreground(lipgloss.Color(cDim)).Padding(0, 1)
	keyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(cOrange)).Bold(true)
	selStyle    = lipgloss.NewStyle().Background(lipgloss.Color("#3A1A00")).Foreground(lipgloss.Color(cText))

	pixelBorder = lipgloss.Border{
		Top: "▀", Bottom: "▄", Left: "▌", Right: "▐",
		TopLeft: "▛", TopRight: "▜", BottomLeft: "▙", BottomRight: "▟",
	}
)

// ---- messages ----

type tickMsg time.Time

type walletDataMsg struct {
	bal    Balance
	height uint64
	addrs  []SubAddr
	txs    []Transfer
	err    error
}

type daemonMsg struct {
	info *DaemonInfo
	addr string
	err  error
}

type preparedMsg struct {
	tx      *PreparedTx
	address string
	err     error
}

type relayedMsg struct {
	hash string
	err  error
}

type statusMsg struct {
	text string
	err  bool
}

type daemonSetMsg struct {
	addr string
	err  error
}

// ---- model ----

type tab int

const (
	tabOverview tab = iota
	tabTxs
	tabReceive
	tabSend
	tabNode
	tabCount
)

var tabNames = []string{"OVERVIEW", "HISTORY", "RECEIVE", "SEND", "NODE"}

type sendStage int

const (
	sendEdit sendStage = iota
	sendPreparing
	sendConfirm
	sendRelaying
)

var priorities = []string{"default", "unimportant", "normal", "elevated"}

type model struct {
	w, h       int
	wallet     *RPC
	daemon     *RPC
	daemonAddr string
	walletName string

	tab     tab
	spin    spinner.Model
	loaded  bool
	fetchW  bool
	fetchD  bool
	updated time.Time

	bal    Balance
	height uint64
	addrs  []SubAddr
	txs    []Transfer
	wErr   error
	dinfo  *DaemonInfo
	dErr   error

	txCursor   int
	txDetail   bool
	recvCursor int
	showQR     bool
	labeling   bool
	labelInput textinput.Model

	sendTo     textinput.Model
	sendAmt    textinput.Model
	sendFocus  int // 0 to, 1 amount, 2 priority
	editing    bool
	priority   int
	stage      sendStage
	prepared   *PreparedTx
	preparedTo string

	nodeInput   textinput.Model
	nodeEditing bool

	status    string
	statusErr bool
	statusAt  time.Time
}

func newInput(placeholder string, width int) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = ""
	ti.Width = width
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(cOrange))
	ti.TextStyle = textStyle
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(cGrey))
	return ti
}

func newModel(wallet *RPC, daemonAddr, walletName string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{Frames: []string{"▖", "▘", "▝", "▗"}, FPS: 150 * time.Millisecond}
	sp.Style = accentStyle
	to := newInput("4… or 8… address", 64)
	to.CharLimit = 106
	amt := newInput("0.0", 20)
	amt.CharLimit = 24
	lbl := newInput("label (optional)", 30)
	lbl.CharLimit = 64
	node := newInput("host:port", 50)
	node.CharLimit = 200
	return model{
		wallet: wallet, daemon: NewRPC(daemonEndpoint(daemonAddr), "", ""),
		daemonAddr: daemonAddr, walletName: walletName,
		spin: sp, showQR: true, priority: 0,
		sendTo: to, sendAmt: amt, labelInput: lbl, nodeInput: node,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, fetchWallet(m.wallet), fetchDaemon(m.daemon, m.daemonAddr), tick())
}

func tick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ---- commands ----

func fetchWallet(w *RPC) tea.Cmd {
	return func() tea.Msg {
		var out walletDataMsg
		ctx, cancel := ctxT(20 * time.Second)
		defer cancel()
		if err := w.Call(ctx, "get_balance", map[string]any{"account_index": 0}, &out.bal); err != nil {
			out.err = err
			return out
		}
		var h struct {
			Height uint64 `json:"height"`
		}
		if err := w.Call(ctx, "get_height", nil, &h); err != nil {
			out.err = err
			return out
		}
		out.height = h.Height
		var a struct {
			Addresses []SubAddr `json:"addresses"`
		}
		if err := w.Call(ctx, "get_address", map[string]any{"account_index": 0}, &a); err != nil {
			out.err = err
			return out
		}
		out.addrs = a.Addresses
		var t struct {
			In, Out, Pending, Failed, Pool []Transfer
		}
		if err := w.Call(ctx, "get_transfers", map[string]any{
			"in": true, "out": true, "pending": true, "failed": true, "pool": true, "account_index": 0,
		}, &t); err != nil {
			out.err = err
			return out
		}
		for _, l := range [][]Transfer{t.In, t.Out, t.Pending, t.Failed, t.Pool} {
			out.txs = append(out.txs, l...)
		}
		sort.SliceStable(out.txs, func(i, j int) bool {
			pi, pj := out.txs[i].Height == 0, out.txs[j].Height == 0
			if pi != pj {
				return pi
			}
			return out.txs[i].Timestamp > out.txs[j].Timestamp
		})
		return out
	}
}

func fetchDaemon(d *RPC, addr string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := ctxT(15 * time.Second)
		defer cancel()
		var info DaemonInfo
		err := d.Call(ctx, "get_info", nil, &info)
		if err != nil {
			return daemonMsg{addr: addr, err: err}
		}
		return daemonMsg{info: &info, addr: addr}
	}
}

func prepareTx(w *RPC, to string, amount uint64, priority int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := ctxT(20 * time.Second)
		var v struct {
			Valid   bool   `json:"valid"`
			Nettype string `json:"nettype"`
		}
		err := w.Call(ctx, "validate_address", map[string]any{"address": to}, &v)
		cancel()
		if err != nil {
			return preparedMsg{err: err}
		}
		if !v.Valid {
			return preparedMsg{err: errors.New("that address is not valid")}
		}
		ctx, cancel = ctxT(2 * time.Minute)
		defer cancel()
		var tx PreparedTx
		err = w.Call(ctx, "transfer", map[string]any{
			"destinations":    []map[string]any{{"amount": amount, "address": to}},
			"account_index":   0,
			"priority":        priority,
			"do_not_relay":    true,
			"get_tx_metadata": true,
			"get_tx_key":      true,
		}, &tx)
		if err != nil {
			return preparedMsg{err: err}
		}
		return preparedMsg{tx: &tx, address: to}
	}
}

func relayTx(w *RPC, meta string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := ctxT(2 * time.Minute)
		defer cancel()
		var r struct {
			TxHash string `json:"tx_hash"`
		}
		err := w.Call(ctx, "relay_tx", map[string]any{"hex": meta}, &r)
		return relayedMsg{hash: r.TxHash, err: err}
	}
}

func createSubaddr(w *RPC, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := ctxT(20 * time.Second)
		defer cancel()
		var r struct {
			Address string `json:"address"`
			Index   uint32 `json:"address_index"`
		}
		if err := w.Call(ctx, "create_address", map[string]any{"account_index": 0, "label": label}, &r); err != nil {
			return statusMsg{text: "could not create subaddress: " + err.Error(), err: true}
		}
		return statusMsg{text: fmt.Sprintf("created subaddress #%d", r.Index)}
	}
}

func setDaemon(w *RPC, addr string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := ctxT(30 * time.Second)
		defer cancel()
		err := w.Call(ctx, "set_daemon", map[string]any{"address": addr, "trusted": false}, nil)
		return daemonSetMsg{addr: addr, err: err}
	}
}

func rescanNow(w *RPC) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := ctxT(5 * time.Minute)
		defer cancel()
		var r struct {
			Blocks uint64 `json:"blocks_fetched"`
		}
		if err := w.Call(ctx, "refresh", nil, &r); err != nil {
			return statusMsg{text: "refresh failed: " + err.Error(), err: true}
		}
		return statusMsg{text: fmt.Sprintf("refreshed — %d new blocks", r.Blocks)}
	}
}

func copyText(s, what string) tea.Cmd {
	return func() tea.Msg {
		for _, c := range [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}} {
			if _, err := exec.LookPath(c[0]); err != nil {
				continue
			}
			cmd := exec.Command(c[0], c[1:]...)
			cmd.Stdin = strings.NewReader(s)
			if err := cmd.Run(); err != nil {
				return statusMsg{text: "copy failed: " + err.Error(), err: true}
			}
			return statusMsg{text: what + " copied to clipboard"}
		}
		return statusMsg{text: "no clipboard tool (wl-copy) found", err: true}
	}
}

// ---- update ----

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.sendTo.Width = min(96, max(20, m.w-24))
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tickMsg:
		cmds := []tea.Cmd{tick()}
		if !m.fetchW {
			m.fetchW = true
			cmds = append(cmds, fetchWallet(m.wallet))
		}
		if !m.fetchD {
			m.fetchD = true
			cmds = append(cmds, fetchDaemon(m.daemon, m.daemonAddr))
		}
		return m, tea.Batch(cmds...)

	case walletDataMsg:
		m.fetchW = false
		m.wErr = msg.err
		if msg.err == nil {
			m.bal, m.height, m.addrs, m.txs = msg.bal, msg.height, msg.addrs, msg.txs
			m.loaded = true
			m.updated = time.Now()
			m.txCursor = clamp(m.txCursor, 0, len(m.txs)-1)
			m.recvCursor = clamp(m.recvCursor, 0, len(m.addrs)-1)
		}
		return m, nil

	case daemonMsg:
		m.fetchD = false
		if msg.addr != m.daemonAddr {
			return m, nil // stale reply from a previous node
		}
		m.dErr = msg.err
		if msg.err == nil {
			m.dinfo = msg.info
		}
		return m, nil

	case statusMsg:
		m.setStatus(msg.text, msg.err)
		return m, fetchWallet(m.wallet)

	case preparedMsg:
		if msg.err != nil {
			m.stage = sendEdit
			m.setStatus("could not build transaction: "+msg.err.Error(), true)
			return m, nil
		}
		m.stage, m.prepared, m.preparedTo = sendConfirm, msg.tx, msg.address
		return m, nil

	case relayedMsg:
		if msg.err != nil {
			m.stage = sendConfirm
			m.setStatus("broadcast failed: "+msg.err.Error(), true)
			return m, nil
		}
		m.stage, m.prepared, m.preparedTo = sendEdit, nil, ""
		m.sendTo.SetValue("")
		m.sendAmt.SetValue("")
		m.setStatus("sent! tx "+short(msg.hash, 10), false)
		m.tab = tabTxs
		m.txCursor = 0
		return m, fetchWallet(m.wallet)

	case daemonSetMsg:
		if msg.err != nil {
			m.setStatus("could not switch node: "+msg.err.Error(), true)
			return m, nil
		}
		m.daemonAddr = msg.addr
		m.daemon = NewRPC(daemonEndpoint(msg.addr), "", "")
		m.dinfo, m.dErr = nil, nil
		m.setStatus("switched node to "+msg.addr, false)
		m.fetchD = true
		return m, tea.Batch(fetchDaemon(m.daemon, m.daemonAddr), fetchWallet(m.wallet))

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) setStatus(s string, isErr bool) {
	m.status, m.statusErr, m.statusAt = s, isErr, time.Now()
}

func (m model) inputActive() bool {
	return m.editing || m.labeling || m.nodeEditing
}

func (m model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	// Text entry modes swallow most keys.
	if m.labeling {
		switch key {
		case "esc":
			m.labeling = false
			m.labelInput.Blur()
			return m, nil
		case "enter":
			m.labeling = false
			m.labelInput.Blur()
			lbl := strings.TrimSpace(m.labelInput.Value())
			m.labelInput.SetValue("")
			return m, createSubaddr(m.wallet, lbl)
		}
		var cmd tea.Cmd
		m.labelInput, cmd = m.labelInput.Update(k)
		return m, cmd
	}
	if m.nodeEditing {
		switch key {
		case "esc":
			m.nodeEditing = false
			m.nodeInput.Blur()
			return m, nil
		case "enter":
			addr := strings.TrimSpace(m.nodeInput.Value())
			m.nodeEditing = false
			m.nodeInput.Blur()
			if addr == "" {
				return m, nil
			}
			m.setStatus("connecting to "+addr+"…", false)
			return m, setDaemon(m.wallet, addr)
		}
		var cmd tea.Cmd
		m.nodeInput, cmd = m.nodeInput.Update(k)
		return m, cmd
	}
	if m.tab == tabSend && m.stage == sendConfirm {
		switch key {
		case "y", "Y":
			m.stage = sendRelaying
			return m, relayTx(m.wallet, m.prepared.TxMetadata)
		case "n", "N", "esc":
			m.stage, m.prepared = sendEdit, nil
			m.setStatus("transfer cancelled — nothing was sent", false)
			return m, nil
		}
		return m, nil
	}
	if m.editing {
		return m.handleSendKey(k)
	}

	// Global navigation.
	switch key {
	case "q":
		return m, tea.Quit
	case "1", "2", "3", "4", "5":
		m.tab = tab(key[0] - '1')
		return m, nil
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "shift+tab", "left", "h":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return m, nil
	case "r":
		m.setStatus("refreshing…", false)
		return m, rescanNow(m.wallet)
	}

	switch m.tab {
	case tabOverview:
		if key == "c" && len(m.addrs) > 0 {
			return m, copyText(m.addrs[0].Address, "primary address")
		}
	case tabTxs:
		switch key {
		case "up", "k":
			m.txCursor = clamp(m.txCursor-1, 0, len(m.txs)-1)
		case "down", "j":
			m.txCursor = clamp(m.txCursor+1, 0, len(m.txs)-1)
		case "g", "home":
			m.txCursor = 0
		case "G", "end":
			m.txCursor = max(0, len(m.txs)-1)
		case "enter", " ":
			m.txDetail = !m.txDetail
		case "c":
			if len(m.txs) > 0 {
				return m, copyText(m.txs[m.txCursor].TxID, "tx id")
			}
		}
	case tabReceive:
		switch key {
		case "up", "k":
			m.recvCursor = clamp(m.recvCursor-1, 0, len(m.addrs)-1)
		case "down", "j":
			m.recvCursor = clamp(m.recvCursor+1, 0, len(m.addrs)-1)
		case "c", "enter":
			if len(m.addrs) > 0 {
				return m, copyText(m.addrs[m.recvCursor].Address, "address")
			}
		case "n":
			m.labeling = true
			return m, m.labelInput.Focus()
		case "v":
			m.showQR = !m.showQR
		}
	case tabSend:
		if m.stage == sendEdit && (key == "enter" || key == "e" || key == "i") {
			m.editing = true
			m.sendFocus = 0
			return m, m.focusSend()
		}
	case tabNode:
		if key == "e" || key == "enter" {
			m.nodeEditing = true
			m.nodeInput.SetValue(m.daemonAddr)
			m.nodeInput.CursorEnd()
			return m, m.nodeInput.Focus()
		}
	}
	return m, nil
}

func (m *model) focusSend() tea.Cmd {
	m.sendTo.Blur()
	m.sendAmt.Blur()
	switch m.sendFocus {
	case 0:
		return m.sendTo.Focus()
	case 1:
		return m.sendAmt.Focus()
	}
	return nil
}

func (m model) handleSendKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.editing = false
		m.sendTo.Blur()
		m.sendAmt.Blur()
		return m, nil
	case "tab", "down":
		m.sendFocus = (m.sendFocus + 1) % 3
		return m, m.focusSend()
	case "shift+tab", "up":
		m.sendFocus = (m.sendFocus + 2) % 3
		return m, m.focusSend()
	case "enter", "ctrl+s":
		if m.sendFocus < 2 && k.String() == "enter" {
			m.sendFocus++
			return m, m.focusSend()
		}
		to := strings.TrimSpace(m.sendTo.Value())
		amt, err := parseXMR(m.sendAmt.Value())
		switch {
		case to == "":
			m.setStatus("enter a destination address", true)
			return m, nil
		case err != nil:
			m.setStatus(err.Error(), true)
			return m, nil
		case m.loaded && amt > m.bal.Unlocked:
			m.setStatus("amount is more than your unlocked balance", true)
			return m, nil
		}
		m.editing = false
		m.sendTo.Blur()
		m.sendAmt.Blur()
		m.stage = sendPreparing
		return m, prepareTx(m.wallet, to, amt, m.priority)
	}
	if m.sendFocus == 2 {
		switch k.String() {
		case "left", "h":
			m.priority = (m.priority + len(priorities) - 1) % len(priorities)
		case "right", "l", " ":
			m.priority = (m.priority + 1) % len(priorities)
		}
		return m, nil
	}
	var cmd tea.Cmd
	if m.sendFocus == 0 {
		m.sendTo, cmd = m.sendTo.Update(k)
	} else {
		m.sendAmt, cmd = m.sendAmt.Update(k)
	}
	return m, cmd
}

// ---- view ----

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	cw := m.w - 4 // content width
	header := m.viewHeader(cw)
	tabs := m.viewTabs(cw)
	footer := m.viewFooter(cw)
	bodyH := m.h - lipgloss.Height(header) - lipgloss.Height(tabs) - lipgloss.Height(footer) - 1

	var body string
	switch m.tab {
	case tabOverview:
		body = m.viewOverview(cw, bodyH)
	case tabTxs:
		body = m.viewTxs(cw, bodyH)
	case tabReceive:
		body = m.viewReceive(cw, bodyH)
	case tabSend:
		body = m.viewSend(cw)
	case tabNode:
		body = m.viewNode(cw)
	}
	body = lipgloss.NewStyle().Height(max(0, bodyH)).MaxHeight(max(0, bodyH)).Render(body)
	out := lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, footer)
	return lipgloss.NewStyle().Margin(0, 2).Render(out)
}

func (m model) viewHeader(cw int) string {
	// Heading options, largest first; the first that fits the width wins.
	// Window widths are cw+4 (the side margins).
	grad := gradient(cOrangeHi, cOrangeLo)
	ansiShadow := shadowArt(moneroWordmark, grad, "#9A3D00")
	pixel5x7 := pixelText("MONERO", 1, grad, cShadow)
	pixel3x5 := small.render("MONERO", 1, grad, "")
	options := []struct {
		word  string
		sym   int // Monero symbol size in pixels (0 = none)
		minCW int
	}{
		{ansiShadow, 12, 0}, // 73+ cols: full symbol
		{ansiShadow, 8, 0},  // 69-72: smaller symbol
		{pixel5x7, 8, 0},    // 50-68: symbol kept, Pixel 5x7
		{pixel5x7, 0, 0},    // 40-49
		{pixel3x5, 0, 0},    // under 40
	}
	logo := pixel3x5
	for _, o := range options {
		w := lipgloss.Width(o.word)
		if o.sym > 0 {
			w += o.sym + 2
		}
		if w <= cw && cw >= o.minCW {
			logo = o.word
			if o.sym > 0 {
				logo = lipgloss.JoinHorizontal(lipgloss.Center, moneroSymbol(o.sym), "  ", o.word)
			}
			break
		}
	}

	dot, state := errStyle.Render("●"), "offline"
	switch {
	case m.dErr == nil && m.dinfo != nil && m.isSynced():
		dot, state = okStyle.Render("●"), "synced"
	case m.dErr == nil && m.dinfo != nil:
		dot, state = warnStyle.Render("●"), "syncing"
	case m.dErr == nil:
		dot, state = m.spin.View(), "connecting"
	}
	info := lipgloss.JoinVertical(lipgloss.Right,
		accentStyle.Render(m.walletName),
		dimStyle.Render(short(m.daemonAddr, 34)),
		dot+" "+textStyle.Render(state),
	)
	if gap := cw - lipgloss.Width(logo) - lipgloss.Width(info); gap >= 2 {
		logo = lipgloss.JoinHorizontal(lipgloss.Bottom, logo, strings.Repeat(" ", gap), info)
	}
	// One blank line of breathing room above the logo, one below.
	return "\n" + logo + "\n"
}

func (m model) viewTabs(cw int) string {
	render := func(names []string) string {
		var parts []string
		for i, n := range names {
			label := fmt.Sprintf("%d %s", i+1, n)
			if tab(i) == m.tab {
				parts = append(parts, tabOn.Render(label))
			} else {
				parts = append(parts, tabOff.Render(label))
			}
		}
		return strings.Join(parts, " ")
	}
	row := render(tabNames)
	if lipgloss.Width(row) > cw {
		row = render([]string{"OVER", "HIST", "RECV", "SEND", "NODE"})
	}
	if lipgloss.Width(row) > cw {
		row = render([]string{"", "", "", "", ""})
	}
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cFrame)).Render(strings.Repeat("▀", max(0, cw)))
	return row + "\n" + rule
}

func (m model) viewFooter(cw int) string {
	var hints []string
	add := func(k, d string) { hints = append(hints, keyStyle.Render(k)+" "+dimStyle.Render(d)) }
	switch {
	case m.labeling || m.nodeEditing:
		add("enter", "confirm")
		add("esc", "cancel")
	case m.editing:
		add("tab", "next field")
		add("←→", "priority")
		add("ctrl+s", "review")
		add("esc", "stop editing")
	case m.tab == tabSend && m.stage == sendConfirm:
		add("y", "broadcast")
		add("n", "cancel")
	default:
		add("1-5", "tabs")
		switch m.tab {
		case tabOverview:
			add("c", "copy address")
		case tabTxs:
			add("↑↓", "move")
			add("enter", "details")
			add("c", "copy txid")
		case tabReceive:
			add("↑↓", "select")
			add("c", "copy")
			add("n", "new subaddress")
			add("v", "QR")
		case tabSend:
			add("enter", "fill in")
		case tabNode:
			add("e", "change node")
		}
		add("r", "refresh")
		add("q", "quit")
	}
	sep := dimStyle.Render("  ·  ")
	left := strings.Join(hints, sep)
	// Too narrow: drop hints from the middle, always keeping the last (quit).
	for len(hints) > 1 && lipgloss.Width(left) > cw {
		hints = append(hints[:len(hints)-2], hints[len(hints)-1])
		left = strings.Join(hints, sep)
	}

	var right string
	if m.status != "" && time.Since(m.statusAt) < 8*time.Second {
		if m.statusErr {
			right = errStyle.Render("✖ " + m.status)
		} else {
			right = okStyle.Render("✔ " + m.status)
		}
	} else if m.wErr != nil {
		right = warnStyle.Render("wallet busy: " + short(m.wErr.Error(), 40))
	} else if !m.updated.IsZero() {
		right = dimStyle.Render("updated " + ago(m.updated))
	}
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cFrame)).Render(strings.Repeat("▄", max(0, cw)))
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > cw {
		return rule + "\n" + left + "\n" + right
	}
	return rule + "\n" + left + strings.Repeat(" ", cw-lipgloss.Width(left)-lipgloss.Width(right)) + right
}

func panel(title, body string, width int, hot bool) string {
	bc := cFrame
	if hot {
		bc = cOrange
	}
	st := lipgloss.NewStyle().Border(pixelBorder).BorderForeground(lipgloss.Color(bc)).Padding(0, 1).Width(max(4, width-2))
	return st.Render(tagStyle.Render(" "+title+" ") + "\n\n" + body)
}

func (m model) isSynced() bool {
	return m.dinfo != nil && m.height > 0 && m.height+1 >= m.dinfo.Height
}

func (m model) viewOverview(cw, h int) string {
	if !m.loaded {
		return "\n  " + m.spin.View() + textStyle.Render(" loading wallet… (first sync can take a minute)")
	}
	wide := cw >= 100
	balW, netW := cw, cw
	if wide {
		balW = cw * 58 / 100
		netW = cw - balW - 1
	}

	// Balance in the big pixel font; the small blocky font only when the
	// panel is too narrow for it, then plain text as a last resort.
	amount := fmtXMRShort(m.bal.Balance)
	inner := balW - 6
	bigView := accentStyle.Render(amount + " XMR")
	for _, f := range []pixelFont{big, small} {
		if f.width(amount, 1, false)+f.width("XMR", 1, false)+2 <= inner {
			num := f.render(amount, 1, gradient(cOrangeHi, cOrangeLo), "")
			unit := f.render("XMR", 1, func(int, int) string { return cGrey }, "")
			bigView = lipgloss.JoinHorizontal(lipgloss.Top, num, "  ", unit)
			break
		}
		if f.h == small.h && f.width(amount, 1, false)+5 <= inner {
			num := f.render(amount, 1, gradient(cOrangeHi, cOrangeLo), "")
			bigView = lipgloss.JoinHorizontal(lipgloss.Bottom, num, dimStyle.Render(" XMR"))
			break
		}
	}
	locked := m.bal.Balance - m.bal.Unlocked
	rows := []string{
		bigView,
		"",
		row("total", textStyle.Render(fmtXMR(m.bal.Balance))+dimStyle.Render(" XMR")),
		row("unlocked", okStyle.Render(fmtXMR(m.bal.Unlocked))+dimStyle.Render(" XMR")),
	}
	if locked > 0 {
		rows = append(rows, row("locked", warnStyle.Render(fmtXMR(locked))+dimStyle.Render(fmt.Sprintf(" XMR · ~%d blocks (~%d min)", m.bal.BlocksToUnlock, m.bal.BlocksToUnlock*2))))
	}
	var top string
	if wide {
		balance := panel("BALANCE", strings.Join(rows, "\n"), balW, true)
		network := panel("NETWORK", m.syncBlock(netW-6), netW, false)
		top = lipgloss.JoinHorizontal(lipgloss.Top, balance, " ", network)
	} else {
		// Too narrow for side by side: stack the network panel underneath,
		// or fold sync status into the balance panel if height is tight.
		balance := panel("BALANCE", strings.Join(rows, "\n"), balW, true)
		network := panel("NETWORK", m.syncBlock(netW-6), netW, false)
		if lipgloss.Height(balance)+lipgloss.Height(network) <= h {
			top = lipgloss.JoinVertical(lipgloss.Left, balance, network)
		} else {
			rows = append(rows, "", m.syncLine(balW-6))
			top = panel("BALANCE", strings.Join(rows, "\n"), balW, true)
		}
	}

	addr := ""
	if len(m.addrs) > 0 {
		addr = row("address", textStyle.Render(middleTrunc(m.addrs[0].Address, cw-16)))
	}
	// Panel chrome (borders, title, address, spacing) costs 7 lines.
	room := h - lipgloss.Height(top) - 7
	if room < 1 {
		return top
	}
	recent := m.txRows(0, min(room, 8, len(m.txs)), cw-6, -1)
	if len(m.txs) == 0 {
		recent = dimStyle.Render("no transactions yet — share your address from the RECEIVE tab")
	}
	activity := panel("RECENT", addr+"\n\n"+recent, cw, false)
	return lipgloss.JoinVertical(lipgloss.Left, top, activity)
}

func (m model) syncBlock(w int) string {
	if m.dErr != nil {
		return errStyle.Render("node unreachable") + "\n" + dimStyle.Render(short(m.dErr.Error(), w)) +
			"\n\n" + dimStyle.Render("try another node in the NODE tab")
	}
	if m.dinfo == nil {
		return m.spin.View() + dimStyle.Render(" contacting node…")
	}
	chain := m.dinfo.Height
	pct := 0.0
	if chain > 0 {
		pct = float64(m.height) / float64(chain)
	}
	if pct > 1 {
		pct = 1
	}
	state := warnStyle.Render("SYNCING")
	if m.isSynced() {
		state, pct = okStyle.Render("SYNCED"), 1
	}
	barW := max(10, w-14)
	return strings.Join([]string{
		row("wallet", textStyle.Render(commas(m.height))),
		row("chain", textStyle.Render(commas(chain))),
		row("network", textStyle.Render(m.dinfo.Nettype)+dimStyle.Render(nodeVer(m.dinfo.Version))),
		"",
		bar(pct, barW) + " " + state,
	}, "\n")
}

// syncLine is the compact one-line sync status used on narrow terminals.
func (m model) syncLine(w int) string {
	switch {
	case m.dErr != nil:
		return errStyle.Render("node unreachable") + dimStyle.Render(" — see NODE tab")
	case m.dinfo == nil:
		return m.spin.View() + dimStyle.Render(" contacting node…")
	}
	pct, state := 0.0, warnStyle.Render("SYNCING")
	if m.dinfo.Height > 0 {
		pct = min(1, float64(m.height)/float64(m.dinfo.Height))
	}
	if m.isSynced() {
		pct, state = 1, okStyle.Render("SYNCED")
	}
	heights := dimStyle.Render(" " + commas(m.height) + "/" + commas(m.dinfo.Height))
	return bar(pct, max(6, w-lipgloss.Width(heights)-9)) + " " + state + heights
}

func nodeVer(v string) string {
	if v == "" {
		return ""
	}
	return " · v" + v
}

func orDash(s string) string {
	if s == "" {
		return "hidden by node"
	}
	return s
}

func bar(pct float64, w int) string {
	fill := int(pct*float64(w) + 0.5)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(cOrange)).Render(strings.Repeat("█", fill)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color(cGrey)).Render(strings.Repeat("░", w-fill))
}

func row(label, value string) string {
	return dimStyle.Render(fmt.Sprintf("%-9s", label)) + " " + value
}

// txRows renders transactions [from, to). sel is the highlighted index (-1 none).
func (m model) txRows(from, to, w, sel int) string {
	var lines []string
	for i := from; i < to; i++ {
		t := m.txs[i]
		icon, amt, col := "▼", "+"+fmtXMR(t.Amount), cGreen
		if t.Type == "out" || t.Type == "pending" || t.Type == "failed" {
			icon, amt, col = "▲", "-"+fmtXMR(t.Amount+t.Fee), cOrange
		}
		conf := fmt.Sprintf("%d conf", t.Confirmations)
		switch {
		case t.Type == "failed":
			conf, col = "FAILED", cRed
		case t.Type == "pool" || t.Type == "pending":
			conf = "pending"
		case t.Confirmations >= 10:
			conf = "✔"
		}
		when := time.Unix(t.Timestamp, 0).Format("2006-01-02 15:04")
		label := t.Note
		if label == "" {
			label = short(t.TxID, 12)
		}
		line := fmt.Sprintf("%s  %s  %20s  %-8s  %s", icon, when, amt, conf, label)
		line = truncate(line, w)
		if i == sel {
			lines = append(lines, selStyle.Width(w).Render(lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Render(line)))
		} else {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Render(line))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) viewTxs(cw, h int) string {
	if len(m.txs) == 0 {
		return panel("HISTORY", dimStyle.Render("no transactions yet"), cw, true)
	}
	detailH := 0
	var detail string
	if m.txDetail {
		detail = m.txDetailView(m.txs[m.txCursor], cw)
		detailH = lipgloss.Height(detail)
	}
	visible := max(1, h-detailH-5)
	start := 0
	if m.txCursor >= visible {
		start = m.txCursor - visible + 1
	}
	end := min(len(m.txs), start+visible)
	head := dimStyle.Render(fmt.Sprintf("   %-16s  %20s  %-8s  %s", "date", "amount", "status", "note / txid"))
	list := panel(fmt.Sprintf("HISTORY %d/%d", m.txCursor+1, len(m.txs)), head+"\n"+m.txRows(start, end, cw-6, m.txCursor), cw, true)
	if detail != "" {
		return lipgloss.JoinVertical(lipgloss.Left, list, detail)
	}
	return list
}

func (m model) txDetailView(t Transfer, cw int) string {
	w := cw - 18
	lines := []string{
		row("type", textStyle.Render(t.Type)),
		row("amount", textStyle.Render(fmtXMR(t.Amount)+" XMR")),
		row("fee", textStyle.Render(fmtXMR(t.Fee)+" XMR")),
		row("height", textStyle.Render(commas(t.Height))),
		row("confirms", textStyle.Render(fmt.Sprint(t.Confirmations))),
		row("txid", textStyle.Render(middleTrunc(t.TxID, w))),
	}
	if t.Address != "" {
		lines = append(lines, row("address", textStyle.Render(middleTrunc(t.Address, w))))
	}
	for _, d := range t.Destinations {
		lines = append(lines, row("to", textStyle.Render(middleTrunc(d.Address, w-24)+"  "+fmtXMR(d.Amount))))
	}
	if t.Note != "" {
		lines = append(lines, row("note", textStyle.Render(t.Note)))
	}
	if t.DoubleSpend {
		lines = append(lines, errStyle.Render("⚠ double spend seen"))
	}
	return panel("DETAILS", strings.Join(lines, "\n"), cw, false)
}

func (m model) viewReceive(cw, h int) string {
	if len(m.addrs) == 0 {
		return "\n  " + m.spin.View() + dimStyle.Render(" loading addresses…")
	}
	sel := m.addrs[m.recvCursor]
	qr := ""
	if m.showQR {
		qr = renderQR("monero:" + sel.Address)
		if lipgloss.Height(qr)+4 > h || lipgloss.Width(qr)+40 > cw {
			qr = ""
		}
	}
	listW := cw
	if qr != "" {
		listW = cw - lipgloss.Width(qr) - 7
	}

	var lines []string
	for i, a := range m.addrs {
		label := a.Label
		if label == "" && a.Index == 0 {
			label = "Primary"
		}
		used := "  "
		if a.Used {
			used = okStyle.Render("✔ ")
		}
		l := fmt.Sprintf("#%-3d %-14s %s", a.Index, truncate(label, 14), middleTrunc(a.Address, max(12, listW-30)))
		if i == m.recvCursor {
			lines = append(lines, used+selStyle.Render(l))
		} else {
			lines = append(lines, used+textStyle.Render(l))
		}
	}
	maxRows := max(3, h-14)
	start := 0
	if m.recvCursor >= maxRows {
		start = m.recvCursor - maxRows + 1
	}
	lines = lines[start:min(len(lines), start+maxRows)]

	full := wrap(sel.Address, listW-6)
	body := strings.Join(lines, "\n") + "\n\n" + dimStyle.Render("selected address") + "\n" + accentStyle.Render(full)
	if m.labeling {
		body += "\n\n" + dimStyle.Render("new subaddress label ▸ ") + m.labelInput.View()
	}
	body += "\n\n" + dimStyle.Render("Use a fresh subaddress per payer for better privacy.")
	list := panel("ADDRESSES", body, listW, true)
	if qr == "" {
		return list
	}
	qrPanel := lipgloss.NewStyle().Border(pixelBorder).BorderForeground(lipgloss.Color(cFrame)).Render(qr)
	return lipgloss.JoinHorizontal(lipgloss.Top, list, " ", qrPanel)
}

func renderQR(s string) string {
	q, err := qrcode.New(s, qrcode.Low)
	if err != nil {
		return ""
	}
	q.DisableBorder = true
	bm := q.Bitmap()
	const quiet = 2
	n := len(bm) + quiet*2
	px := make([][]string, n+n%2)
	for y := range px {
		px[y] = make([]string, n)
		for x := range px[y] {
			by, bx := y-quiet, x-quiet
			dark := by >= 0 && by < len(bm) && bx >= 0 && bx < len(bm) && bm[by][bx]
			if dark {
				px[y][x] = "#000000"
			} else {
				px[y][x] = "#FFFFFF"
			}
		}
	}
	if n%2 == 1 {
		for x := range px[n] {
			px[n][x] = "#FFFFFF"
		}
	}
	return renderHalfBlocks(px)
}

func (m model) viewSend(cw int) string {
	switch m.stage {
	case sendPreparing:
		return panel("SEND", m.spin.View()+textStyle.Render(" building transaction & estimating fee…"), cw, true)
	case sendRelaying:
		return panel("SEND", m.spin.View()+textStyle.Render(" broadcasting to the network…"), cw, true)
	case sendConfirm:
		t := m.prepared
		body := strings.Join([]string{
			row("amount", accentStyle.Render(fmtXMR(t.Amount)+" XMR")),
			row("fee", textStyle.Render(fmtXMR(t.Fee)+" XMR")),
			row("total", accentStyle.Render(fmtXMR(t.Amount+t.Fee)+" XMR")),
			row("priority", textStyle.Render(priorities[m.priority])),
			"",
			dimStyle.Render("to"),
			textStyle.Render(wrap(m.preparedTo, cw-8)),
			"",
			warnStyle.Render("Double-check the address. Monero transfers cannot be reversed."),
			"",
			keyStyle.Render("[Y]") + textStyle.Render(" broadcast   ") + keyStyle.Render("[N]") + textStyle.Render(" cancel"),
		}, "\n")
		return panel("REVIEW TRANSFER", body, cw, true)
	}

	field := func(i int, label, content string) string {
		marker := "  "
		lbl := dimStyle.Render(fmt.Sprintf("%-9s", label))
		if m.editing && m.sendFocus == i {
			marker = accentStyle.Render("▶ ")
			lbl = accentStyle.Render(fmt.Sprintf("%-9s", label))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, marker+lbl+" ", content)
	}
	box := func(v string, w int) string {
		return lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(lipgloss.Color(cGrey)).Width(w).Render(v)
	}
	prio := dimStyle.Render("◀ ") + accentStyle.Render(fmt.Sprintf("%-11s", priorities[m.priority])) + dimStyle.Render(" ▶")
	avail := dimStyle.Render("available " + fmtXMR(m.bal.Unlocked) + " XMR")
	body := strings.Join([]string{
		field(0, "to", box(m.sendTo.View(), m.sendTo.Width+1)),
		"",
		field(1, "amount", lipgloss.JoinHorizontal(lipgloss.Top, box(m.sendAmt.View(), 22), dimStyle.Render(" XMR   ")+avail)),
		"",
		field(2, "priority", prio+dimStyle.Render("   higher = faster confirmation, bigger fee")),
		"",
		"",
	}, "\n")
	if m.editing {
		body += dimStyle.Render("Press ") + keyStyle.Render("ctrl+s") + dimStyle.Render(" (or enter on priority) to review. Nothing is sent until you confirm.")
	} else {
		body += dimStyle.Render("Press ") + keyStyle.Render("enter") + dimStyle.Render(" to start filling in the form.")
	}
	return panel("SEND XMR", body, cw, true)
}

func (m model) viewNode(cw int) string {
	var lines []string
	lines = append(lines, row("address", accentStyle.Render(m.daemonAddr)), row("mode", textStyle.Render("remote · untrusted")))
	switch {
	case m.dErr != nil:
		lines = append(lines, row("status", errStyle.Render("unreachable")), "", dimStyle.Render(wrap(m.dErr.Error(), cw-8)))
	case m.dinfo == nil:
		lines = append(lines, row("status", m.spin.View()+" connecting"))
	default:
		d := m.dinfo
		st := okStyle.Render(d.Status)
		if d.Status != "OK" {
			st = warnStyle.Render(d.Status)
		}
		synced := okStyle.Render("yes")
		if !d.Synchronized {
			synced = warnStyle.Render("no")
		}
		lines = append(lines,
			row("status", st),
			row("version", textStyle.Render(orDash(d.Version))),
			row("network", textStyle.Render(d.Nettype)),
			row("height", textStyle.Render(commas(d.Height))),
			row("synced", synced),
			row("peers", textStyle.Render(fmt.Sprintf("%d out / %d in", d.Outgoing, d.Incoming))),
			row("mempool", textStyle.Render(fmt.Sprintf("%d txs", d.TxPoolSize))),
		)
	}
	lines = append(lines, "")
	if m.nodeEditing {
		lines = append(lines, accentStyle.Render("new node ▸ ")+m.nodeInput.View())
	} else {
		lines = append(lines, keyStyle.Render("[E]")+textStyle.Render(" switch node"))
	}
	lines = append(lines, "",
		dimStyle.Render("A remote node can see your IP and when you transact, but never your keys or balance."),
		dimStyle.Render("Find more nodes at monero.fail — or point this at your own monerod later."))
	return panel("NODE", strings.Join(lines, "\n"), cw, true)
}

// ---- formatting helpers ----

const atomic = 1_000_000_000_000

func fmtXMR(a uint64) string {
	return fmt.Sprintf("%d.%012d", a/atomic, a%atomic)
}

// fmtXMRShort truncates (never rounds up) to 4 decimals for the big display.
func fmtXMRShort(a uint64) string {
	return fmt.Sprintf("%d.%04d", a/atomic, (a%atomic)/100_000_000)
}

func parseXMR(s string) (uint64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, errors.New("enter an amount")
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 12 {
		return 0, errors.New("max 12 decimal places")
	}
	frac += strings.Repeat("0", 12-len(frac))
	w, err1 := strconv.ParseUint(whole, 10, 64)
	f, err2 := strconv.ParseUint(frac, 10, 64)
	if err1 != nil || err2 != nil || w > 18_000_000 {
		return 0, errors.New("invalid amount")
	}
	v := w*atomic + f
	if v == 0 {
		return 0, errors.New("amount must be more than zero")
	}
	return v, nil
}

func commas(n uint64) string {
	s := strconv.FormatUint(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 2 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func truncate(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > n {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func middleTrunc(s string, n int) string {
	if len(s) <= n || n < 9 {
		return s
	}
	h := (n - 1) / 2
	return s[:h] + "…" + s[len(s)-(n-1-h):]
}

func wrap(s string, w int) string {
	if w < 8 {
		w = 8
	}
	var parts []string
	for len(s) > w {
		parts = append(parts, s[:w])
		s = s[w:]
	}
	return strings.Join(append(parts, s), "\n")
}

func ago(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm ago", int(d.Minutes()))
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(hi, v))
}
