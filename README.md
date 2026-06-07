# Immortal Agent

Immortal Agent is a small Go agent runtime that stays alive after a model finishes a response. It accepts work from long-running input loops, sends that work through an OpenAI-compatible chat-completions model with tools, persists conversation state, and then waits for the next event.

The current project has three primary input paths:

- **TUI Mode**: A high-performance terminal interface built with Bubble Tea for interactive usage.
- **Telegram polling**: When `TELEGRAM_BOT_TOKEN` is set, run a Telegram bot loop and answer messages in Telegram.
- **File polling**: Read tasks from `messages.txt`, process them, and append answers to `responses.txt`.

It is still intentionally small. There is no web server, no framework layer, no queue service, and no separate worker process.

## Motive

The main goal is to make an agent process that does not die after one final model response.

In many agent setups, a run starts with one input, the model thinks, calls tools if needed, emits a final response, and the run is over. After that final response, the model cannot wake itself back up. Something outside the run has to start a new run.

Immortal Agent keeps the outer process alive. One LLM/tool loop can finish, but the Go process keeps waiting for more input. A file write, Telegram message, or internal scheduled task can wake the agent again.

## Architecture

`main.go` coordinates multiple goroutines:
1. **Event Bus**: Asynchronous channel handling (`utils.Event`) that routes inputs to the agent.
2. **runAgent**: The core orchestrator that manages SQLite persistence and the LLM tool-calling loop.
3. **TUI**: A Model-Update-View interface (`tui/`) for local interaction.
4. **Telegram Bot**: Background polling for messaging and media handling.
5. **Schedulers**: Background tickers for both Telegram and local task execution.

## Provider

The active client is in `utils/openai.go`.

It uses `github.com/openai/openai-go/v3` with a configurable base URL and model:

```bash
./agent --base-url https://openrouter.ai/api/v1 --model deepseek-v4-flash
```

Defaults:
- base URL: `https://openrouter.ai/api/v1`
- model: `deepseek-v4-flash`
- API key environment variable: `IMMORTAL_API_KEY`

## Quick Start

```bash
export IMMORTAL_API_KEY="..."

go build -o agent
./agent
```

By default, `./agent` will start in **TUI mode** if a terminal is detected.

## Telegram

Telegram support starts automatically when `TELEGRAM_BOT_TOKEN` is set.

Features:
- **Voice Transcription**: Voice messages are transcribed using Groq Whisper (`GROQ_API_KEY` required).
- **Media Tools**: The agent can send documents and images back via `send_document_over_telegram` and `send_image_over_telegram`.
- **Scheduled Tasks**: Ephemeral task firing with the `⏰` prefix.

## Persistent State

State is stored in SQLite at `~/.immortal-agent.db`. Conversations are partitioned by `channel` (e.g., `default`, `tui:default`, or `telegram:<chat_id>`).

## Tools

### Core Tools
- `bash_tool`: Run local commands with 30s timeout.
- `spawn_agents`: Parallelize tasks using sub-agents.
- `web_search`: Search via Jina (`JINA_API_KEY` required).
- `url_fetch`: Read URL content via Jina.
- `mail`: Full AgentMail inbox management.

### Scheduling Tools
- **Telegram**: `schedule_task`, `cancel_task`, `list_scheduled_tasks`.
- **Local**: `local_schedule_task`, `local_cancel_task`, `local_list_scheduled_tasks`.

## Files

```text
.
├── main.go             # Lifecycle, DB init, event routing
├── tui/                # Bubble Tea TUI components and styling
└── utils/
    ├── db.go           # SQLite conversation persistence
    ├── openai.go       # Tool schemas and LLM loop
    ├── telegram.go     # Bot logic and voice transcription
    ├── tool_mail.go    # AgentMail integration
    ├── tools_manager.go # Tool dispatcher
    ├── local_scheduler.go # Background local task manager
    └── scheduler.go    # Telegram task manager
```

## Requirements

- Go `1.25.10`
- `IMMORTAL_API_KEY`
- Bash
- Optional: `TELEGRAM_BOT_TOKEN`, `GROQ_API_KEY`, `JINA_API_KEY`, `AGENT_MAIL_API_KEY`.

## License

MIT.
