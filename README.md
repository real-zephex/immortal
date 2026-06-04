# Immortal Agent

**A single-binary, file-polling AI agent with parallel sub-agent spawning, built in Go.**

Immortal Agent is not a framework. It is not a DSL. It does not require Python, pip, virtualenvs, or 400 dependencies. It is a single compiled binary that reads tasks from a text file, routes them through an LLM with tool access, and writes responses to another file. That is the entire surface area.

---

## Why another AI agent?

Every AI agent framework in 2026 — LangChain, CrewAI, AutoGPT, OpenAI Agents SDK, Claude Agent SDK — shares a common DNA: they are Python or TypeScript libraries with deep abstraction stacks. You install hundreds of packages, learn opaque APIs, fight breaking changes on every version bump, and ultimately ship an agent that lives inside a framework you don't control.

Immortal Agent takes the opposite approach:

| Dimension | LangChain / CrewAI / AutoGPT | OpenAI Agents SDK | Immortal Agent |
|-----------|-----------------------------|-------------------|----------------|
| **Language** | Python / TypeScript | Python / TypeScript | **Go** |
| **Deployment** | pip install + venv + 300+ deps | pip install + venv | **Single binary** |
| **Interface** | Python SDK / API | SDK + API | **File-based (messages.txt → responses.txt)** |
| **Agent loop** | Framework-managed (LangGraph graph, CrewAI process) | SDK-managed (Runner.run) | **Roll-your-own loop (predictable, debuggable)** |
| **Tool execution** | Synchronous by default (even CrewAI runs tools sequentially per agent) | Synchronous by default (parallel tool execution is a feature request) | **Parallel via goroutines** |
| **Sub-agents** | Built-in (CrewAI crews, LangGraph subgraphs, AutoGPT tasks) | Handoffs (transfers control, no concurrency) | **true concurrent goroutines** |
| **Startup time** | 2-5s (Python interpreter + imports) | 1-3s (Python interpreter) | **~5ms** |
| **Memory footprint** | 150-400 MB | 100-300 MB | **~15 MB** |
| **State management** | Framework-managed (conversation buffers, vector stores) | SDK-managed (session, to_input_list, previous_response_id) | **Explicit — you control what goes in the next call** |
| **Observability** | OpenTelemetry / LangSmith / vendor SDKs | OpenAI Traces / third-party | **Plain stdout + file logs** |
| **API stability** | Breaking changes every few months | Stable but evolving | **Zero abstraction — you control the HTTP** |

### What the comparisons don't tell you

**LangChain / LangGraph** has 600+ integrations, but it also has 600+ abstractions you have to learn. The graph-based agent loop is powerful — if your use case requires branching state machines, conditional edges, and human-in-the-loop checkpoints. LangGraph does that well. But most agent tasks do not need a DAG. Most agent tasks need "call LLM → execute tool → call LLM again → return." LangGraph's boilerplate for that loop is absurd.

**CrewAI** is the closest philosophical cousin to Immortal Agent's sub-agent system. You define agents with roles, assign them tasks, and a crew manager orchestrates. The insight is correct — parallel specialized agents beat monolithic generalists. In practice, CrewAI agents execute tools **sequentially within each agent** (a known limitation), and the crew orchestration runs inside a single Python process with the GIL. Immortal Agent's sub-agents are true OS-level goroutines with real parallelism.

**AutoGPT** pioneered the autonomous loop, but it is not production-grade in 2026. Infinite retry loops, runaway API costs, and unpredictable behavior are well-documented failure modes. It remains a prototyping tool.

**OpenAI Agents SDK** is lean by modern standards (four primitives, one runner, one decorator). The handoff pattern transfers control between specialized agents, but does not run them concurrently — Agent A stops, Agent B starts. Immortal Agent's sub-agents all run simultaneously.

**Claude Agent SDK** wraps Claude Code's capabilities programmatically. It is single-threaded by design (Anthropic's "nO master loop"), with limited sub-agent spawning for controlled parallelism. The architecture is intentionally simple, similar in philosophy to Immortal Agent — but tied to Claude and npm/PyPI.

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    immortal-agent                     │
│                                                       │
│  messages.txt  ──►  PushToChannelA  ──►  runAgent    │
│                   (polls every 5s)     (event loop)   │
│                                            │          │
│                                            ▼          │
│                                     OpenAIManager()    │
│                                     ┌──────────┐      │
│                                     │ Loop:     │      │
│                                     │ LLM call  │      │
│                                     │ tool?     │      │
│                                     │ execute   │◄──── │
│                                     │ append    │      │
│                                     │ repeat    │      │
│                                     └──────────┘      │
│                                            │          │
│                              ExecuteTool()            │
│                              ┌──────────────┐         │
│                              │ bash_tool     │         │
│                              │ spawn_agents  │──┐      │
│                              └──────────────┘  │      │
│                                                 │      │
│                                   ┌─────────────┴───┐  │
│                                   │ goroutine 1     │  │
│                                   │ goroutine 2     │  │
│                                   │ goroutine N     │  │
│                                   │ each calls      │  │
│                                   │ OpenAIManager   │  │
│                                   │ with bash_tool  │  │
│                                   └─────────────────┘  │
│                                            │          │
│                              WaitGroup sync            │
│                              results converge         │
│                                            │          │
│                                            ▼          │
│                                orchestrator LLM       │
│                              re-invoked with results  │
│                                            │          │
│                                            ▼          │
│  responses.txt  ◄──── final response                 │
└─────────────────────────────────────────────────────┘
```

### The loop (the important part)

Immortal Agent uses a standard ReAct loop, but with a critical property: **every tool call result is appended to the message history, and the LLM is re-invoked immediately**. This repeats until the LLM produces a text response with no tool calls. The loop is time-boxed to 40 iterations to prevent runaway costs.

Most frameworks do the same thing, but they bury it behind abstractions. Here it is explicit:

```go
for range maxToolIterations {
    response := llm.Call(messages, tools)
    messages = append(messages, response.Message)

    if len(response.ToolCalls) == 0 {
        return response.Content  // done
    }

    for _, tool := range response.ToolCalls {
        result := ExecuteTool(tool.Name, tool.Args)
        messages = append(messages, ToolMessage(result, tool.ID))
    }
    // loop continues — LLM sees tool results and decides next action
}
```

### Sub-agents (true parallelism)

The `spawn_agents` tool is the key differentiator. When the orchestrator LLM decides a task can be parallelized, it calls this tool with a list of sub-tasks:

```json
{
  "sub_agents": [
    {"id": 0, "task": "Check Go version"},
    {"id": 1, "task": "Check disk space"},
    {"id": 2, "task": "Check memory usage"}
  ],
  "reason": "Need system information in parallel"
}
```

Each sub-agent gets its own goroutine, its own LLM conversation (with a stripped system prompt), and its own tool access (`bash_tool`). They run concurrently and results converge via `sync.WaitGroup`. Failed agents are reported inline, not silently dropped.

This is architecturally different from the handoff pattern (OpenAI Agents SDK), the sequential-within-agent pattern (CrewAI), or the single-threaded loop (Claude Code).

---

## Quick Start

```bash
# Set your API key (uses Groq's OpenAI-compatible endpoint by default)
export GROQ_API_KEY="gsk-..."

# Build
go build -o agent

# Run (it will poll messages.txt every 5 seconds)
./agent

# In another terminal, write tasks
echo "List the files in the current directory" > messages.txt

# Responses appear in responses.txt
```

### Test with the included prompts

```bash
cat > messages.txt << 'EOF'
Spawn sub-agents to check Go version and disk space in parallel
Use bash to check what OS this is
EOF
```

---

## Project Structure

```
.
├── main.go                    # Entry point: event loop, signal handling, file polling
├── messages.txt               # Input pipe — newline-separated tasks
├── responses.txt              # Output log — timestamped User/Assistant exchanges
├── utils/
│   ├── openai.go              # LLM client, tool definitions, agent loop, sub-agent prompt
│   ├── groq.go                # (deprecated) raw Groq HTTP client, no tool support
│   └── tools_manager.go       # Tool dispatch: bash_tool, spawn_agents
├── go.mod                     # Module: immortal, Go 1.25.10
└── go.sum
```

---

## Key Design Decisions

### File-based IPC (not HTTP, not WebSocket, not gRPC)

`messages.txt` and `responses.txt` are the interface. This is intentional:
- **Zero infrastructure** — no server to run, no ports to manage, no API to secure
- **Composable** — pipe tasks from cron, CI/CD, shell scripts, or any language
- **Observable by default** — the entire conversation history is in a plain text file
- **Pausable** — stop the agent, edit messages.txt, restart. It picks up where it left off

### Single-threaded orchestrator

The orchestrator runs in one goroutine with one conversation history. Concurrency is handled by sub-agents, not by adding `aiWorkers`. This avoids:
- Conversation history corruption (shared state between parallel LLM calls)
- Rate limit contention (multiple orchestrators hitting the same API endpoint)
- Non-deterministic response ordering

### Stripped sub-agent prompts

Sub-agents get a minimal system prompt: "You are a sub-agent focused on completing the assigned task. You have access to bash tools. Respond concisely with only your findings. Do not ask follow-up questions or request additional tasks." This prevents sub-agents from wandering into meta-conversations.

### Tool results are opaque strings

Tools return `(string, error)`. The orchestrator LLM receives the raw tool output in a tool-role message. There is no schema enforcement, no structured output parsing, no middleware. If the tool returns garbage, the LLM sees garbage. This is a deliberate simplicity trade.

---

## Requirements

- Go 1.25+
- `GROQ_API_KEY` environment variable (or any OpenAI-compatible endpoint — modify `BASE_URL` in `openai.go:64`)
- Linux (for `bash_tool`; macOS should work, Windows needs WSL)

---

## License

MIT. Do what you want.
