# Immortal Agent

Immortal Agent is a small Go agent runtime that stays alive after a model finishes a response. It accepts work from long-running input loops, sends that work through an OpenAI-compatible chat-completions model with tools, persists conversation state, and then waits for the next event.

The current project has two input paths:

- file polling: read tasks from `messages.txt`, process them, and append answers to `responses.txt`
- Telegram polling: when `TELEGRAM_BOT_TOKEN` is set, run a Telegram bot loop and answer messages in Telegram

It is still intentionally small. There is no web server, no framework layer, no queue service, and no separate worker process.

## Motive

The main goal is to make an agent process that does not die after one final model response.

In many agent setups, a run starts with one input, the model thinks, calls tools if needed, emits a final response, and the run is over. After that final response, the model cannot wake itself back up. Something outside the run has to start a new run.

Immortal Agent keeps the outer process alive. One LLM/tool loop can finish, but the Go process keeps waiting for more input. A file write, Telegram message, or future event source can wake the agent again.

That is the core shape: event source in, model/tool loop runs, response goes out, process remains alive.

## Current Shape

```text
                    ┌────────────────────────────┐
messages.txt ────► │ file polling goroutine     │
                    │ PushToChannelA             │
                    └─────────────┬──────────────┘
                                  │
                                  v
                    ┌────────────────────────────┐
                    │ main event channel          │
                    └─────────────┬──────────────┘
                                  │
                                  v
                    ┌────────────────────────────┐
                    │ runAgent                    │
                    │ channel: "default"          │
                    └─────────────┬──────────────┘
                                  │
                                  v
                    ┌────────────────────────────┐
                    │ SQLite conversation state   │
                    │ ~/.immortal-agent.db        │
                    └─────────────┬──────────────┘
                                  │
                                  v
                    ┌────────────────────────────┐
                    │ OpenAI-compatible LLM loop  │
                    │ tools allowed               │
                    └─────────────┬──────────────┘
                                  │
                                  v
responses.txt ◄──────────────── final answer


Telegram, if enabled:

Telegram updates ─► StartTelegramBot ─► channel: "telegram:<chat_id>"
                                  │
                                  v
                    same SQLite state + same LLM loop
                                  │
                                  v
                    Telegram reply / document / image
```

`main.go` starts the file polling path by default. If `TELEGRAM_BOT_TOKEN` exists, it also starts the Telegram bot in another goroutine.

The process exits only on `SIGINT`, `SIGTERM`, or startup failure.

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

This is not tied to OpenRouter. Any OpenAI-compatible provider can be used if it supports chat completions and the tool-calling format expected by `openai-go`.

## Quick Start

```bash
export IMMORTAL_API_KEY="..."

go build -o agent
./agent
```

In another terminal:

```bash
echo "Use bash to list the files in this project" > messages.txt
```

The agent polls `messages.txt` every 5 seconds, clears it after reading, and writes the final answer to `responses.txt`.

Queue multiple tasks with one task per non-empty line:

```bash
cat > messages.txt <<'EOF'
Use bash to print the current directory
Spawn sub-agents to check Go version, disk space, and uptime in parallel
EOF
```

Clear persisted conversation history:

```bash
./agent --clear
```

## Telegram

Telegram support starts automatically when `TELEGRAM_BOT_TOKEN` is set:

```bash
export TELEGRAM_BOT_TOKEN="..."
./agent
```

Telegram behavior:

- `/start` and `/help` are handled directly.
- Normal text messages are sent to the LLM loop.
- Each Telegram chat gets its own persisted channel: `telegram:<chat_id>`.
- Replies include the replied-to message text as context.
- Long responses are split into Telegram-sized chunks.
- Markdown output is converted to sanitized Telegram HTML.
- Voice messages are downloaded from Telegram and transcribed with Groq Whisper.
- The model can send files or images back to the current Telegram chat through Telegram-only tools.

Voice transcription requires:

```bash
export GROQ_API_KEY="..."
```

## Persistent State

Conversation state is stored in SQLite at:

```text
~/.immortal-agent.db
```

The `conversations` table stores one JSON message array per channel:

- `default` for the file polling path
- `telegram:<chat_id>` for Telegram chats

The saved message array includes user messages, assistant messages, tool calls, and tool results. `responses.txt` is only a log; it is not used to restore state.

## Tools

### Core Tools

These are available to the file-polling orchestrator:

- `bash_tool`: run `bash -c <command>` with a 30 second timeout and 100 KiB output cap.
- `spawn_agents`: run multiple sub-agent LLM conversations concurrently.
- `web_search`: search the web through Jina.
- `url_fetch`: fetch URL content through Jina.
- `mail`: manage AgentMail inbox threads and messages.

Sub-agents get a smaller tool set:

- `bash_tool`
- `web_search`
- `url_fetch`

Telegram conversations get the core tools plus:

- `send_document_over_telegram`
- `send_image_over_telegram`

### `bash_tool`

```json
{
  "command": "string",
  "reason": "string"
}
```

This is not sandboxed by the project. Commands run with the permissions of the `agent` process.

### `spawn_agents`

```json
{
  "sub_agents": [
    {
      "id": 1,
      "task": "Check the Go version"
    }
  ],
  "reason": "Why these tasks should run in parallel"
}
```

Each sub-agent gets a fresh conversation with `SubAgentSystemPrompt`, runs in its own goroutine, and returns its result to the orchestrator as one combined tool response.

### `web_search`

```json
{
  "topic": "string"
}
```

Uses Jina search at `https://s.jina.ai`. Requires:

```bash
export JINA_API_KEY="..."
```

### `url_fetch`

```json
{
  "url": "string"
}
```

Uses Jina Reader at `https://r.jina.ai`. Requires `JINA_API_KEY`.

### `mail`

```json
{
  "action": "get_threads | get_thread | send_email | reply_to_message | forward_message | delete_thread"
}
```

Backed by AgentMail at `https://api.agentmail.to/v0`.

Requires:

```bash
export AGENT_MAIL_API_KEY="..."
export INBOX_NAME="..."
```

Supported actions:

- `get_threads`
- `get_thread` with `thread_id`
- `send_email` with `to`, `subject`, and `text` or `html`
- `reply_to_message` with `message_id`, `to`, `reply_to`, and `text` or `html`
- `forward_message` with `message_id` and `to`
- `delete_thread` with `thread_id`

### Telegram Send Tools

Only useful during a Telegram conversation, because they rely on the current Telegram chat ID.

```json
{
  "filepath": "/absolute/or/relative/path"
}
```

Tools:

- `send_document_over_telegram`
- `send_image_over_telegram`

## LLM Loop

The core loop lives in `openAIManagerWithTools`:

1. Send the current message history and tool definitions to the model.
2. Append the assistant message to the same history.
3. If there are no tool calls, return the assistant text.
4. If there are tool calls, execute each one.
5. Append each tool result as a tool message.
6. Repeat, up to 40 iterations.

The active request context is passed into `openaiClient.Chat.Completions.New`, so shutdown cancellation can stop in-flight top-level model calls. Sub-agents currently use `context.Background()`.

## Files

```text
.
├── main.go
│   └── flags, lifecycle, DB init, file event channel, optional Telegram startup
├── utils/
│   ├── db.go
│   │   └── SQLite conversation persistence
│   ├── openai.go
│   │   └── OpenAI-compatible client setup, tool schemas, LLM loop
│   ├── telegram.go
│   │   └── Telegram polling, replies, voice transcription, media sending
│   ├── tool_mail.go
│   │   └── AgentMail API tool
│   ├── tools_manager.go
│   │   └── tool dispatcher, Bash, sub-agents, Jina search/fetch, Telegram send tools
│   └── groq.go
│       └── older raw chat-completions client
├── go.mod
└── go.sum
```

Runtime files:

- `messages.txt`: file input source, cleared after polling
- `responses.txt`: append-only file output log
- `~/.immortal-agent.db`: persisted conversation state

## Design Notes

- The process is resident. Final model responses do not stop the agent process.
- The file path and Telegram path both reuse the same LLM/tool loop, but they have different input and output handling.
- File polling uses a single `default` conversation channel.
- Telegram uses one conversation channel per chat ID.
- Conversation history is durable across restarts through SQLite.
- `responses.txt` can grow indefinitely.
- `messages.txt` is destructive input. Once read, it is cleared.
- `bash_tool` is powerful and unsandboxed.
- `CurrentTelegramChatID` is global, so concurrent Telegram chats sending media at the same time could race.
- Sub-agents do not inherit the parent cancellation context.

## Requirements

- Go `1.25.10` as declared in `go.mod`
- `IMMORTAL_API_KEY`
- Bash

Optional features need their own keys:

- `TELEGRAM_BOT_TOKEN` for Telegram
- `GROQ_API_KEY` for Telegram voice transcription
- `JINA_API_KEY` for `web_search` and `url_fetch`
- `AGENT_MAIL_API_KEY` and `INBOX_NAME` for `mail`

## Build

```bash
go build -o agent
```

## Run

```bash
./agent
```

With a custom OpenAI-compatible provider:

```bash
./agent --base-url https://api.example.com/v1 --model provider/model-name
```

Stop with `Ctrl-C`.

## License

MIT.
