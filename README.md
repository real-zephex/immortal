# Immortal Agent

Immortal Agent is a small Go agent that watches `messages.txt`, sends each non-empty line to an LLM, lets the LLM call tools, and appends the final answer to `responses.txt`.

The project is intentionally narrow. There is no HTTP server, no CLI parser, no database, no vector store, and no framework layer. The current binary is a file-polling orchestrator with two tools:

- `bash_tool`: run a Bash command with a 30 second timeout.
- `spawn_agents`: run multiple sub-agent LLM conversations concurrently, each with access to `bash_tool`.

## Motive

The main goal is to make an agent process that does not die after one final model response.

In many agent setups, a run starts with one input, the model thinks, calls tools if needed, emits a final response, and the run is over. After that final response, the model cannot wake itself back up. Something outside the run has to start a new run.

Immortal Agent keeps the outer process alive. The LLM/tool loop can finish for one task, but `runAgent` keeps waiting on the main event channel. Any new event pushed into that channel wakes the agent again with the existing in-memory conversation history.

`messages.txt` is just the current event source. The same shape could support other inputs. For example, a Telegram polling loop could run in a separate goroutine and push incoming Telegram messages into the event channel. Sending the response back to Telegram would need its own output handling, but the core trigger stays the same: push an event, wake the agent, let it work, then keep the process alive for the next event.

## Current Shape

```text
messages.txt
    |
    | polled every ~5 seconds, then cleared
    v
main.go
    |
    | appends user messages to one shared conversation
    v
utils.OpenAIManager
    |
    | calls OpenRouter through openai-go
    | model: openai/gpt-oss-120b:free
    v
tool loop
    |
    | no tool calls: return assistant text
    | tool calls: execute tools, append tool results, call model again
    v
responses.txt
```

`main.go` starts two goroutines:

- `PushToChannelA` reads `messages.txt`, splits it by newline, clears the file, and pushes each non-empty line onto an event channel.
- `runAgent` consumes those events one at a time, keeps a shared OpenAI chat history in memory, calls the LLM/tool loop, and appends timestamped results to `responses.txt`.

The agent stays alive until it receives `SIGINT` or `SIGTERM`.

## Provider

The active client is in `utils/openai.go`.

It uses the official `github.com/openai/openai-go/v3` SDK against OpenRouter:

```go
option.WithBaseURL("https://openrouter.ai/api/v1")
Model: "openai/gpt-oss-120b:free"
```

Required environment variable:

```bash
export OPENROUTER_KEY="..."
```

This is not tied to OpenRouter at the architecture level. The project uses OpenAI-compatible chat completions and tool calling, so any provider with a compatible API can be used by changing `InitOpenAIClient`, the base URL, the model name, and the environment variable read by `GetGroqKey`.

There is also a `utils/groq.go` file with a raw Groq chat-completions client, but it is not used by `main.go` and does not support the current tool loop.

## Quick Start

```bash
export OPENROUTER_KEY="..."

go build -o agent
./agent
```

In another terminal:

```bash
echo "Use bash to list the files in this project" > messages.txt
```

The agent will clear `messages.txt` after reading it and append the answer to `responses.txt`.

You can queue multiple tasks by writing one task per line:

```bash
cat > messages.txt <<'EOF'
Use bash to print the current directory
Spawn sub-agents to check Go version, disk space, and uptime in parallel
EOF
```

## Tools

### `bash_tool`

Schema:

```json
{
  "command": "string",
  "reason": "string"
}
```

Behavior:

- Runs `bash -c <command>`.
- Captures stdout and stderr.
- Adds process errors to the returned output instead of failing silently.
- Kills the command after 30 seconds.
- Truncates output above 100 KiB.

This is not sandboxed by the project. The command runs with the permissions of the `agent` process.

### `spawn_agents`

Schema:

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

Behavior:

- Parses the requested sub-agent tasks.
- Starts one goroutine per sub-agent.
- Gives each sub-agent a fresh chat history:
  - system prompt: `SubAgentSystemPrompt`
  - user message: the sub-task
- Allows sub-agents to use only `bash_tool`, not `spawn_agents`.
- Waits for all goroutines with `sync.WaitGroup`.
- Returns each result in the original task order.

The orchestrator receives the combined sub-agent output as a normal tool result, then calls the LLM again to summarize or continue.

## LLM Loop

The core loop lives in `openAIManagerWithTools`:

1. Send the current message history and tool definitions to the model.
2. Append the assistant message to the same history.
3. If there are no tool calls, return the assistant text.
4. If there are tool calls, execute each one.
5. Append each tool result as a tool message.
6. Repeat, up to 40 iterations.

This is the important behavior: tool results are not terminal output. They become part of the conversation and the model gets another turn to decide what to do next.

## Files

```text
.
├── main.go
│   └── process lifecycle, file polling, event channel, response logging
├── messages.txt
│   └── input file; one task per non-empty line
├── responses.txt
│   └── append-only response log
├── utils/
│   ├── openai.go
│   │   └── OpenRouter client setup, OpenAI message helpers, tool schemas, LLM loop
│   ├── tools_manager.go
│   │   └── tool dispatcher, Bash execution, parallel sub-agent execution
│   └── groq.go
│       └── unused older Groq HTTP client
├── go.mod
└── go.sum
```

## Design Notes

- The orchestrator is single-consumer by design. `runAgent` processes events sequentially so one shared conversation history does not get mutated by multiple workers at once.
- Conversation history is in memory only. `responses.txt` is a log, not a source of restored state after restart.
- `messages.txt` is destructive input. Once read, it is cleared.
- `responses.txt` is append-only and can grow indefinitely.
- The `ctx` passed into `OpenAIManager` is currently not used for the OpenAI request; the request uses `context.TODO()`.
- Sub-agents use `context.Background()`, so they do not inherit shutdown cancellation.
- The project assumes a Unix-like environment with `bash`.

## Requirements

- Go `1.25.10` as declared in `go.mod`
- `OPENROUTER_KEY` for the current OpenRouter configuration, or equivalent credentials after adapting `InitOpenAIClient` for another OpenAI-compatible provider
- Bash

## Build

```bash
go build -o agent
```

## Run

```bash
./agent
```

Stop with `Ctrl-C`.

## License

MIT.
