# 🧬 Immortal Agent

An autonomous, persistent Go-based AI agent designed for long-term operations, multi-channel communication, and complex task orchestration.

## 🚀 Overview

Immortal Agent is a self-hosted AI companion that combines a high-performance terminal interface with background automation. It operates across multiple platforms (Terminal, Telegram, Email) and maintains a persistent memory of all interactions using SQLite.

## ✨ Key Features

- **Autonomous Task Execution**: Capable of running bash commands, performing web searches, and fetching URL content independently.
- **Multi-Agent Orchestration**: Can spawn sub-agents to parallelize complex research or processing tasks.
- **Persistent Memory**: Uses a local SQLite database (`~/.immortal-agent.db`) to maintain context across restarts and different communication channels.
- **Modern TUI**: A beautiful, terminal-native user interface built with **Bubble Tea** and **Lip Gloss**, featuring the **Catppuccin Mocha** palette.
- **Telegram Integration**: Operates as a fully functional Telegram bot with support for voice message transcription, document/image transfers, and ephemeral task notifications.
- **Email Management**: Integrates with the **AgentMail** API to manage threads, reply to messages, and forward emails.
- **Smart Scheduling**: Built-in support for one-shot and recurring tasks (daily, weekly, etc.) that fire automatically in the background.

## 🛠 Architecture

### 🏗 User Interface (MUV)
The TUI follows the **Model-Update-View** architecture. It uses an asynchronous event bus to handle user input, AI responses, and background system logs simultaneously without freezing the interface.

### 🔌 Tool System
Tools are modular and dynamically dispatched. The agent has direct access to:
- **Bash**: Local command execution with safety timeouts.
- **Search**: Web-scale search and content extraction via **Jina AI**.
- **Mail**: Full inbox management via **AgentMail**.
- **Messaging**: Interaction via the **Telegram Bot API**.

### 💾 Persistence Layer
Every message and tool call is serialized as JSON and stored in a keyed conversation table. This allows the agent to resume a specific context based on the input channel (e.g., `tui:default` or `telegram:user_id`).

## ⚙️ Configuration

The agent uses flags and environment variables for setup:

| Flag | Default | Description |
|------|---------|-------------|
| `--model` | `deepseek-v4-flash` | The LLM model to use |
| `--base-url` | `https://openrouter.ai/api/v1` | LLM API provider base URL |
| `--clear` | `false` | Wipe all conversation history |
| `--no-tg` | `false` | Disable Telegram bot even if token is set |

### Environment Variables
- `OPENAI_API_KEY`: Required for the primary LLM logic.
- `TELEGRAM_BOT_TOKEN`: Required to enable the Telegram interface.
- `GROQ_API_KEY`: Used for high-speed voice transcription (Whisper).
- `JINA_API_KEY`: Powering web search and URL fetching.
- `AGENTMAIL_TOKEN`: Required for email management tools.

## 📦 Dependencies

Built with a robust Go stack:
- **UI**: `bubbletea`, `lipgloss`, `glamour`
- **Database**: `modernc.org/sqlite`
- **SDKs**: `openai-go`, `telegram-bot-api`

---
*Immortal Agent: Designed for persistence. Built for autonomy.*
