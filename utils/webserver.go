package utils

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// --- WebSocket protocol -------------------------------------------------
//
// Client -> Server:
//   {"type":"user","content":"<message>"}
//
// Server -> Client:
//   {"type":"history","messages":[...]}        // full conversation on connect
//   {"type":"user","content":"..."}            // echo of user message
//   {"type":"intermediate","content":"..."}    // model's in-progress text
//   {"type":"tool_call","tool":"...","summary":"...","details":"..."}
//   {"type":"status","content":"..."}
//   {"type":"assistant","content":"..."}       // final assistant response
//   {"type":"error","content":"..."}
// ------------------------------------------------------------------------------

const (
	wsReadBuffer  = 1024
	wsWriteBuffer = 1024
	wsChannel     = "default" // same channel as TUI
	wsPort        = ":8080"
)

// wsClient represents a single connected browser.
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// wsWebServer holds all state for the web mode.
type WebServer struct {
	mu      sync.Mutex
	clients map[*wsClient]bool
	events  chan<- Event
	db      *sql.DB
	Model   string // model name displayed in the UI header

	upgrader websocket.Upgrader
}

// Envelope is the JSON message sent over the wire.
type Envelope struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Summary string `json:"summary,omitempty"`
	Details string `json:"details,omitempty"`
}

// NewWebServer creates a WebServer that writes user messages into events.
func NewWebServer(events chan<- Event, db *sql.DB) *WebServer {
	return &WebServer{
		clients: make(map[*wsClient]bool),
		events:  events,
		db:      db,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Start launches the HTTP + WebSocket server and blocks until ctx is cancelled.
func (ws *WebServer) Start(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", ws.handleWebSocket)

	// Serve a small static web UI from memory.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := strings.ReplaceAll(string(indexHTML), "__MODEL__", ws.Model)
		w.Write([]byte(html))
	})

	srv := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	fmt.Println("Web server started on http://localhost:8080")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("Web server error: %v", err)
	}
}

// SetupHooks wires the global hooks to broadcast to WebSocket clients.
// This should be called after NewWebServer and before starting the agent loop.
func (ws *WebServer) SetupHooks() {
	// Save original hooks
	origPrintHook := PrintHook
	origStatusHook := StatusHook
	origIntermediateHook := IntermediateHook
	origResponseHook := ResponseHook

	PrintHook = func(text string) {
		// Also call original hook (for TUI if running)
		if origPrintHook != nil {
			origPrintHook(text)
		}
		// Parse tool call format and broadcast
		if strings.HasPrefix(text, "TOOL:") {
			parts := strings.SplitN(text, "|||", 3)
			if len(parts) == 3 {
				tool := strings.TrimPrefix(parts[0], "TOOL:")
				log.Printf("[web] ⚡ tool_call tool=%s summary=%s", tool, parts[1])
				ws.Broadcast(Envelope{
					Type:    "tool_call",
					Tool:    tool,
					Summary: parts[1],
					Details: parts[2],
				})
				return
			}
		}
		log.Printf("[web] status: %s", strings.TrimSpace(text))
		ws.Broadcast(Envelope{Type: "status", Content: text})
	}

	StatusHook = func(status string) {
		if origStatusHook != nil {
			origStatusHook(status)
		}
		log.Printf("[web] status: %s", status)
		ws.Broadcast(Envelope{Type: "status", Content: status})
	}

	IntermediateHook = func(text string) {
		if origIntermediateHook != nil {
			origIntermediateHook(text)
		}
		log.Printf("[web] intermediate: %s", text)
		ws.Broadcast(Envelope{Type: "intermediate", Content: text})
	}

	ResponseHook = func(text string) {
		if origResponseHook != nil {
			origResponseHook(text)
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		log.Printf("[web] ✦ assistant reply: %s", text)
		ws.Broadcast(Envelope{Type: "assistant", Content: text})
	}
}

func (ws *WebServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	client := &wsClient{conn: conn, send: make(chan []byte, 256)}
	ws.mu.Lock()
	ws.clients[client] = true
	clientCount := len(ws.clients)
	ws.mu.Unlock()
	log.Printf("[web] client connected (%d active) from %s", clientCount, r.RemoteAddr)

	// On connect, push the full-conversation history (same DB as TUI).
	ws.sendHistory(client)

	go ws.writePump(client)
	go ws.readPump(client)
}

// writePump drains client.send and writes to the socket.
func (ws *WebServer) writePump(c *wsClient) {
	defer func() {
		c.conn.Close()
		ws.mu.Lock()
		delete(ws.clients, c)
		ws.mu.Unlock()
	}()

	for msg := range c.send {
		c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// readPump reads incoming user messages and dispatches them to the agent.
func (ws *WebServer) readPump(c *wsClient) {
	defer func() {
		c.conn.Close()
		ws.mu.Lock()
		delete(ws.clients, c)
		clientCount := len(ws.clients)
		ws.mu.Unlock()
		close(c.send)
		log.Printf("[web] client disconnected (%d active)", clientCount)
	}()

	c.conn.SetReadLimit(1 << 20) // 1MB
	c.conn.SetReadDeadline(time.Now().Add(60 * 60 * time.Second))

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			log.Printf("[web] could not parse ws message: %s", string(data))
			continue
		}

		if env.Type != "chat_message" || strings.TrimSpace(env.Content) == "" {
			log.Printf("[web] ignoring ws message type=%q", env.Type)
			continue
		}

		log.Printf("[web] ← user message: %s", env.Content)

		// Echo back so the UI can render it in a chat bubble.
		ws.Broadcast(Envelope{Type: "user", Content: env.Content})

		select {
		case ws.events <- Event{Type: EventTypeUserMessage, Payload: env.Content}:
			log.Printf("[web] → dispatched to agent")
		case <-time.After(2 * time.Second):
			log.Printf("[web] !! event channel full, dropped user message")
		}
	}
}

func (ws *WebServer) sendHistory(c *wsClient) {
	params := LoadConversation(ws.db, wsChannel)
	env := Envelope{Type: "history"}
	if params != nil {
		jsonData, _ := json.Marshal(params)
		env.Content = string(jsonData)
	}
	data, _ := json.Marshal(env)
	select {
	case c.send <- data:
	case <-time.After(5 * time.Second):
	}
}

// Broadcast sends an envelope to every connected client.
func (ws *WebServer) Broadcast(env Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for c := range ws.clients {
		select {
		case c.send <- data:
		default:
		}
	}
}

const wsIndexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Immortal Agent</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Sora:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
<style>
:root{
  --bg:#000;
  --surface:#0c0c0e;
  --panel:#131317;
  --border:rgba(255,255,255,.07);
  --border-strong:rgba(255,255,255,.13);
  --text:#ececee;
  --dim:#a2a2ab;
  --faint:#63636d;
  --accent:#cba6f7;
  --accent-soft:rgba(203,166,247,.10);
  --teal:#94e2d5;
  --red:#f38ba8;
  --green:#5fe48c;
  --sans:'Sora',ui-sans-serif,system-ui,-apple-system,sans-serif;
  --mono:'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,monospace;
}
*{box-sizing:border-box}
html,body{height:100%}
body{
  margin:0;
  background:
    radial-gradient(1200px 500px at 50% -10%, rgba(203,166,247,.06), transparent 60%),
    var(--bg);
  color:var(--text);
  font-family:var(--sans);
  -webkit-font-smoothing:antialiased;
  overflow:hidden;
}
body::after{
  content:"";
  position:fixed;inset:0;
  pointer-events:none;
  opacity:.04;
  z-index:90;
  background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='160' height='160'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.8' numOctaves='2'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E");
}
::-webkit-scrollbar{width:8px;height:8px}
::-webkit-scrollbar-track{background:transparent}
::-webkit-scrollbar-thumb{background:rgba(255,255,255,.10);border-radius:99px}
::-webkit-scrollbar-thumb:hover{background:rgba(255,255,255,.2)}
#app{display:flex;height:100vh;overflow:hidden}

/* ---------- sidebar ---------- */
#sidebar{
  width:264px;flex:0 0 264px;height:100vh;
  background:var(--surface);
  border-right:1px solid var(--border);
  display:flex;flex-direction:column;
  z-index:30;
  transition:transform .25s ease;
}
.sb-inner{display:flex;flex-direction:column;height:100%;padding:12px}
#newChat{
  display:flex;align-items:center;gap:8px;width:100%;
  background:transparent;color:var(--text);
  border:1px solid var(--border-strong);
  border-radius:10px;padding:10px 12px;
  font-family:inherit;font-size:13px;cursor:pointer;
  transition:background .15s,border-color .15s;
}
#newChat:hover{background:rgba(255,255,255,.05);border-color:rgba(203,166,247,.4)}
#newChat svg{color:var(--accent)}
.sb-label{color:var(--faint);font-size:10.5px;text-transform:uppercase;letter-spacing:.1em;padding:16px 10px 8px}
.conv{
  display:flex;align-items:center;gap:10px;width:100%;
  padding:9px 10px;border-radius:8px;
  color:var(--dim);font-size:13px;cursor:pointer;
  border:none;background:transparent;font-family:inherit;text-align:left;
  transition:background .12s,color .12s;
}
.conv:hover{background:rgba(255,255,255,.04);color:var(--text)}
.conv.active{background:var(--accent-soft);color:var(--text)}
.conv.active .cIcon{color:var(--accent)}
.cIcon{flex:0 0 auto;font-size:12px;color:var(--faint)}
.cName{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.cTime{flex:0 0 auto;color:var(--faint);font-size:11px}
.sb-foot{margin-top:auto;border-top:1px solid var(--border);padding:10px 4px 0}
.sb-user{display:flex;align-items:center;gap:10px;padding:6px 6px}
.sb-user .uAv{
  width:28px;height:28px;flex:0 0 28px;border-radius:50%;
  background:linear-gradient(135deg,#89b4fa,#5f8fe0);
  color:#0a0a0c;display:flex;align-items:center;justify-content:center;
  font-size:12px;font-weight:700;
}
.sb-user .who{font-size:13px;font-weight:500}
.sb-user .sub{font-size:11px;color:var(--faint)}

/* ---------- main column ---------- */
#main{flex:1;display:flex;flex-direction:column;min-width:0;position:relative}
#topbar{
  height:54px;flex:0 0 54px;
  display:flex;align-items:center;gap:12px;padding:0 18px;
  border-bottom:1px solid var(--border);
  background:rgba(0,0,0,.6);
  backdrop-filter:blur(14px);
  -webkit-backdrop-filter:blur(14px);
  z-index:20;
}
#menuBtn{display:none;width:34px;height:34px;align-items:center;justify-content:center;background:transparent;border:1px solid var(--border);border-radius:8px;color:var(--dim);cursor:pointer}
.brand{display:flex;align-items:center;gap:10px;font-size:14.5px;font-weight:600;letter-spacing:.2px}
.brand .mark{
  width:27px;height:27px;border-radius:8px;
  background:linear-gradient(135deg,#cba6f7,#7f5fd0);
  color:#0a0a0c;display:flex;align-items:center;justify-content:center;font-size:13px;
  box-shadow:0 2px 12px rgba(203,166,247,.35);
}
.modelTag{font-family:var(--mono);font-size:11px;color:var(--faint);border:1px solid var(--border);padding:3px 9px;border-radius:99px;margin-left:4px}
.spacer{flex:1}
.statusPill{
  display:flex;align-items:center;gap:7px;
  font-size:12px;color:var(--dim);
  border:1px solid var(--border);border-radius:99px;padding:5px 12px;
  background:var(--panel);
}
.statusPill .dot{width:7px;height:7px;border-radius:50%;background:var(--green);box-shadow:0 0 8px rgba(95,228,140,.9);animation:pulse 2.2s ease-in-out infinite}
@keyframes pulse{50%{opacity:.35}}

/* ---------- messages ---------- */
#msgs{flex:1;overflow-y:auto;padding:20px 0 12px}
.chat{max-width:780px;margin:0 auto;padding:0 20px}
.row{display:flex;gap:12px;margin:0 0 26px;animation:rise .3s ease both}
@keyframes rise{from{opacity:0;transform:translateY(8px)}to{opacity:1;transform:none}}
.avatar{
  flex:0 0 30px;width:30px;height:30px;border-radius:9px;
  background:linear-gradient(135deg,rgba(203,166,247,.2),rgba(203,166,247,.05));
  border:1px solid rgba(203,166,247,.28);
  display:flex;align-items:center;justify-content:center;
  color:var(--accent);font-size:13px;margin-top:2px;
}
.a-text{flex:1;min-width:0;word-break:break-word;line-height:1.65;font-size:14.5px;padding-top:5px}
.a-text .plain{white-space:pre-wrap}
.a-text p{margin:0 0 12px}
.a-text p:last-child{margin-bottom:0}
.a-text h1,.a-text h2,.a-text h3,.a-text h4{margin:20px 0 10px;font-weight:600;line-height:1.3;letter-spacing:.01em}
.a-text h1{font-size:1.35em}.a-text h2{font-size:1.22em}.a-text h3{font-size:1.1em}
.a-text ul,.a-text ol{margin:0 0 12px;padding-left:22px}
.a-text li{margin:4px 0}
.a-text ul li::marker,.a-text ol li::marker{color:var(--accent)}
.a-text strong{color:#fff;font-weight:600}
.a-text a{color:var(--accent);text-decoration:none;border-bottom:1px solid rgba(203,166,247,.3)}
.a-text a:hover{border-bottom-color:var(--accent)}
.a-text code{font-family:var(--mono);font-size:.86em;background:rgba(255,255,255,.07);border:1px solid var(--border);padding:1.5px 5px;border-radius:5px;color:#f5e6ff}
.a-text pre{background:#08080a;border:1px solid var(--border);border-radius:10px;padding:12px 14px;overflow-x:auto;margin:0 0 12px}
.a-text pre code{background:none;border:none;padding:0;color:#e4e4e7;font-size:12.5px;line-height:1.6;display:block}
.a-text blockquote{margin:0 0 12px;padding:2px 0 2px 14px;border-left:3px solid var(--accent);color:var(--dim)}
.a-text table{border-collapse:collapse;margin:0 0 12px;font-size:13px;width:100%}
.a-text th,.a-text td{border:1px solid var(--border-strong);padding:6px 10px;text-align:left}
.a-text th{background:var(--panel);color:var(--text)}
.a-text hr{border:none;border-top:1px solid var(--border-strong);margin:16px 0}
.streaming::after{content:"▍";color:var(--accent);margin-left:2px;animation:blink 1s step-start infinite}
@keyframes blink{50%{opacity:0}}

/* user bubble */
.row.user{justify-content:flex-end}
.u-text{
  max-width:82%;
  background:#1b1b20;
  border:1px solid rgba(255,255,255,.05);
  color:var(--text);
  padding:10px 15px;
  border-radius:16px 16px 5px 16px;
  white-space:pre-wrap;word-break:break-word;line-height:1.55;font-size:14.5px;
}
.err{
  background:rgba(244,120,148,.08);
  border:1px solid rgba(244,120,148,.22);
  border-radius:12px;padding:10px 14px;
  color:var(--red);font-size:13px;white-space:pre-wrap;word-break:break-word;
}

/* typing dots */
.think-label{color:var(--faint);font-size:12.5px;font-style:italic;margin-right:8px}
.dots{display:inline-flex;gap:5px;align-items:center;padding:8px 2px}
.dots span{width:7px;height:7px;border-radius:50%;background:var(--accent);opacity:.5;animation:hop 1s ease-in-out infinite}
.dots span:nth-child(2){animation-delay:.15s}
.dots span:nth-child(3){animation-delay:.3s}
@keyframes hop{0%,100%{transform:translateY(0);opacity:.4}50%{transform:translateY(-4px);opacity:1}}

/* status line */
.s-text{color:var(--faint);font-size:12.5px;font-style:italic;padding:2px 0 2px 42px}

/* tool disclosure */
.toolBox{
  flex:1;min-width:0;
  border:1px solid var(--border);
  border-radius:11px;
  background:var(--surface);
  overflow:hidden;
}
.toolHead{
  display:flex;align-items:center;gap:9px;width:100%;
  background:transparent;border:none;color:var(--dim);
  padding:9px 12px;cursor:pointer;font-family:var(--sans);font-size:13px;
  transition:background .12s;
}
.toolHead:hover{background:rgba(255,255,255,.03)}
.tIcon{color:var(--teal);font-size:12px}
.tName{font-family:var(--mono);font-size:12px;color:var(--text);flex:0 0 auto}
.tSum{flex:1;text-align:left;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--faint);min-width:0}
.tChev{flex:0 0 auto;transition:transform .2s;color:var(--faint);font-size:11px}
.toolBox.open .tChev{transform:rotate(180deg)}
.toolBody{display:none;padding:11px 13px;border-top:1px solid var(--border);background:#08080a}
.toolBox.open .toolBody{display:block;animation:rise .18s ease both}
.toolBody pre{white-space:pre-wrap;word-break:break-word;font-family:var(--mono);font-size:12px;color:var(--dim);margin:0;line-height:1.55}

/* ---------- composer ---------- */
#composer{padding:6px 16px 16px;position:relative}
.composerBox{
  max-width:780px;margin:0 auto;
  display:flex;align-items:flex-end;gap:8px;
  background:var(--panel);
  border:1px solid var(--border-strong);
  border-radius:22px;
  padding:9px 9px 9px 18px;
  box-shadow:0 10px 40px rgba(0,0,0,.65);
  transition:border-color .15s,box-shadow .15s;
}
.composerBox:focus-within{
  border-color:rgba(203,166,247,.45);
  box-shadow:0 10px 40px rgba(0,0,0,.65),0 0 0 1px rgba(203,166,247,.12);
}
textarea#inp{
  flex:1;border:none;outline:none;resize:none;
  background:transparent;color:var(--text);
  font-family:inherit;font-size:14.5px;line-height:1.5;
  min-height:24px;max-height:200px;padding:4px 0;
}
textarea#inp::placeholder{color:var(--faint)}
#btn{
  width:34px;height:34px;flex:0 0 34px;border-radius:50%;
  border:none;background:var(--accent);color:#0a0a0c;
  cursor:pointer;display:flex;align-items:center;justify-content:center;
  transition:transform .15s,background .15s,opacity .15s;
}
#btn:hover{background:#e1cdff;transform:scale(1.07)}
#btn:active{transform:scale(.95)}
#btn:disabled{opacity:.35;cursor:not-allowed;transform:none}
#hint{max-width:780px;margin:9px auto 0;text-align:center;color:var(--faint);font-size:11px;letter-spacing:.02em}

/* empty state */
.empty{text-align:center;padding:12vh 20px 0;animation:rise .4s ease both}
.empty-mark{
  width:52px;height:52px;margin:0 auto 16px;border-radius:16px;
  background:linear-gradient(135deg,rgba(203,166,247,.2),rgba(203,166,247,.05));
  border:1px solid rgba(203,166,247,.3);
  display:flex;align-items:center;justify-content:center;
  color:var(--accent);font-size:22px;
}
.empty-title{font-size:17px;font-weight:600;margin-bottom:6px}
.empty-sub{color:var(--faint);font-size:13px}

/* backdrop + mobile */
#backdrop{position:fixed;inset:0;background:rgba(0,0,0,.6);z-index:25;opacity:0;pointer-events:none;transition:opacity .25s}
#backdrop.on{opacity:1;pointer-events:auto}
@media(max-width:820px){
  #sidebar{position:fixed;left:0;top:0;bottom:0;transform:translateX(-104%);box-shadow:24px 0 60px rgba(0,0,0,.7)}
  #sidebar.open{transform:none}
  #menuBtn{display:flex}
  .modelTag{display:none}
}
@media(prefers-reduced-motion:reduce){
  *{animation-duration:.01ms!important;transition-duration:.01ms!important}
}
</style>
</head>
<body>
<div id="app">
  <aside id="sidebar">
    <div class="sb-inner">
      <button id="newChat">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M8 2v12M2 8h12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
        New chat
      </button>
      <div class="sb-label">Conversations</div>
      <div class="conv active" data-name="Immortal agent">
        <span class="cIcon">✦</span>
        <span class="cName">Immortal agent</span>
        <span class="cTime">now</span>
      </div>
      <div class="sb-foot">
        <div class="sb-user">
          <div class="uAv">I</div>
          <div>
            <div class="who">Local operator</div>
            <div class="sub">console session</div>
          </div>
        </div>
      </div>
    </div>
  </aside>
  <div id="backdrop"></div>
  <main id="main">
    <header id="topbar">
      <button id="menuBtn" aria-label="Menu">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M2 4h12M2 8h12M2 12h12" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>
      </button>
      <div class="brand">
        <span class="mark">✦</span>
        <span>Immortal</span>
        <span class="modelTag">__MODEL__</span>
      </div>
      <div class="spacer"></div>
      <div class="statusPill"><span class="dot"></span>online</div>
    </header>

    <div id="msgs"><div class="chat" id="chat"></div></div>

    <div id="composer">
      <div class="composerBox">
        <textarea id="inp" rows="1" placeholder="Message Immortal..."></textarea>
        <button id="btn" disabled aria-label="Send">
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none"><path d="M8 2v11M3.5 7.5 8 12l4.5-4.5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </button>
      </div>
      <div id="hint">Immortal can make mistakes — verify important information.</div>
    </div>
  </main>
</div>
<script src="https://cdn.jsdelivr.net/npm/marked@12.0.2/marked.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/dompurify@3.1.6/dist/purify.min.js"></script>
<script>
(function(){
'use strict';
var chat = document.getElementById('chat');
var inp = document.getElementById('inp');
var btn = document.getElementById('btn');
var sidebar = document.getElementById('sidebar');
var backdrop = document.getElementById('backdrop');
var menuBtn = document.getElementById('menuBtn');
var newChat = document.getElementById('newChat');
var toolCount = 0;
var liveEl = null;

function scrollBottom(){ chat.scrollIntoView({block:'end'}); }
function esc(s){ var d=document.createElement('div'); d.textContent=s; return d.innerHTML; }

function renderMD(text){
  if(window.marked && window.DOMPurify){
    try{
      var html = window.marked.parse(text, {breaks:true});
      html = window.DOMPurify.sanitize(html);
      return html.replace(/<a href=/g,'<a target="_blank" rel="noopener noreferrer" href=');
    }catch(_){}
  }
  return '<div class="plain">'+esc(text)+'</div>';
}

function addRow(cls, innerHTML){
  var row = document.createElement('div');
  row.className = 'row ' + cls;
  row.innerHTML = innerHTML;
  chat.appendChild(row);
  scrollBottom();
  return row;
}

function addUser(content){
  addRow('user','<div class="u-text">'+esc(content)+'</div>');
}

function addAssistant(content, streaming){
  var rendered = renderMD(content);
  if(liveEl && liveEl.dataset.stream === '1'){
    liveEl.querySelector('.a-text').innerHTML = rendered;
    if(!streaming){ liveEl.dataset.stream='0'; liveEl.querySelector('.a-text').classList.remove('streaming'); }
    scrollBottom();
    return liveEl;
  }
  var row = addRow('assistant',
    '<div class="avatar">✦</div>'+
    '<div class="a-text'+(streaming?' streaming':'')+'">'+rendered+'</div>');
  row.dataset.stream = streaming ? '1' : '0';
  liveEl = streaming ? row : null;
  return row;
}

function addStream(content){
  return addAssistant(content, true);
}

function addTyping(){
  var row = addRow('assistant',
    '<div class="avatar">✦</div>'+
    '<div class="a-text"><span class="think-label">Thinking</span><span class="dots"><span></span><span></span><span></span></span></div>');
  row.dataset.typing = '1';
  return row;
}
function removeTyping(){
  var t = chat.querySelector('.row[data-typing="1"]');
  if(t){ t.parentNode.removeChild(t); }
}

function addTool(tool, summary, details){
  removeTyping();
  toolCount++;
  var id = 'tb'+toolCount;
  var body = details ? '<pre>'+esc(details)+'</pre>' : '<pre>'+esc(summary)+'</pre>';
  var row = addRow('tool',
    '<div class="toolBox" id="'+id+'Box">'+
      '<button class="toolHead" onclick="window.toggleTool(\''+id+'\')">'+
        '<span class="tIcon">⚡</span>'+
        '<span class="tName">'+esc(tool)+'</span>'+
        '<span class="tSum">'+esc(summary)+'</span>'+
        '<span class="tChev">▼</span>'+
      '</button>'+
      '<div class="toolBody" id="'+id+'">'+body+'</div>'+
    '</div>');
  return row;
}

function addStatus(content){
  if(content.charAt(0) === '⚡') return;
  removeTyping();
  addRow('status','<div class="s-text">'+esc(content)+'</div>');
}

function addError(content){
  removeTyping();
  if(liveEl){ liveEl.dataset.stream='0'; liveEl=null; }
  addRow('error','<div class="err">✕ '+esc(content)+'</div>');
}

function renderHistory(params){
  if(!Array.isArray(params)) return;
  for(var i=0;i<params.length;i++){
    var m = params[i];
    if(m.role==='user') addUser(m.content || '');
    else if(m.role==='assistant' && m.content) addAssistant(m.content, false);
  }
}

function send(){
  var val = inp.value;
  if(!val.trim()) return;
  ws.send(JSON.stringify({type:'chat_message', content:val}));
  inp.value='';
  autosize();
  updateBtn();
  addTyping();
}

function autosize(){
  inp.style.height = 'auto';
  inp.style.height = Math.min(inp.scrollHeight, 200) + 'px';
}
function updateBtn(){ btn.disabled = inp.value.trim().length === 0; }

window.toggleTool = function(id){
  var box = document.getElementById(id+'Box');
  if(box) box.classList.toggle('open');
};

/* --- websocket --- */
var ws = null;
function connect(){
  ws = new WebSocket((location.protocol==="https:"?"wss://":"ws://") + location.host + "/ws");
  ws.onmessage = function(e){
    var msg;
    try { msg = JSON.parse(e.data); } catch(_){ return; }
    removeTyping();
    if(msg.type === 'history'){
      var params = msg.content;
      if(typeof params === 'string'){
        try{ params = JSON.parse(params); }catch(_){ params = null; }
      }
      renderHistory(params);
      return;
    }
    if(msg.type === 'user'){
      addUser(msg.content || '');
      addTyping();
      return;
    }
    if(msg.type === 'assistant'){ if(msg.content) addAssistant(msg.content, false); return; }
    if(msg.type === 'intermediate'){ if(msg.content) addStream(msg.content); return; }
    if(msg.type === 'tool_call'){ addTool(msg.tool || 'tool', msg.summary || '', msg.details || ''); return; }
    if(msg.type === 'status'){ addStatus(msg.content || ''); return; }
    if(msg.type === 'error'){ addError(msg.content || ''); return; }
  };
  ws.onclose = function(){ setTimeout(connect, 2000); };
  ws.onerror = function(){ try{ ws.close(); }catch(_){ } };
}
connect();

/* --- interactions --- */
btn.addEventListener('click', send);
inp.addEventListener('input', function(){ autosize(); updateBtn(); });
inp.addEventListener('keydown', function(e){
  if(e.key === 'Enter' && !e.shiftKey){ e.preventDefault(); send(); }
});
newChat.addEventListener('click', function(){
  liveEl = null;
  chat.innerHTML = '';
  var empty = document.createElement('div');
  empty.className = 'empty';
  empty.innerHTML = '<div class="empty-mark">✦</div><div class="empty-title">New conversation</div><div class="empty-sub">Fresh thread on screen. Server-side history is preserved.</div>';
  chat.appendChild(empty);
  inp.focus();
  if(window.innerWidth <= 820){
    sidebar.classList.remove('open');
    backdrop.classList.remove('on');
  }
});
menuBtn.addEventListener('click', function(){
  sidebar.classList.toggle('open');
  backdrop.classList.toggle('on');
});
backdrop.addEventListener('click', function(){
  sidebar.classList.remove('open');
  backdrop.classList.remove('on');
});
inp.focus();
})();
</script>
</body>
</html>`

var indexHTML = []byte(wsIndexHTML)
