# cursortab.nvim

A Neovim plugin that provides edit completions and cursor predictions. Currently
supports custom models and models form Zeta (Zed) and SweepAI.

> [!WARNING]
>
> **This is an early-stage, beta project.** Expect bugs, incomplete features,
> and breaking changes.

<p align="center">
    <img src="assets/demo.gif" width="600">
</p>

## Installation

### Using [lazy.nvim](https://github.com/folke/lazy.nvim)

```lua
{
  "leonardcser/cursortab.nvim",
  build = "cd server && go build",
  config = function()
    require("cursortab").setup()
  end,
}
```

### Using [packer.nvim](https://github.com/wbthomason/packer.nvim)

```lua
use {
  "leonardcser/cursortab.nvim",
  run = "cd server && go build",
  config = function()
    require("cursortab").setup()
  end
}
```

## Configuration

```lua
require("cursortab").setup({
  enabled = true,
  log_level = "info",  -- "debug", "info", "warn", "error"

  ui = {
    colors = {
      deletion = "#4f2f2f",      -- Background color for deletions
      addition = "#394f2f",      -- Background color for additions
      modification = "#282e38",  -- Background color for modifications
      completion = "#80899c",    -- Foreground color for completions
    },
    jump = {
      symbol = "",              -- Symbol shown for jump points
      text = " TAB ",            -- Text displayed after jump symbol
      show_distance = true,      -- Show line distance for off-screen jumps
      bg_color = "#373b45",      -- Jump text background color
      fg_color = "#bac1d1",      -- Jump text foreground color
    },
  },

  behavior = {
    idle_completion_delay = 50,  -- Delay in ms after idle to trigger completion (-1 to disable)
    text_change_debounce = 50,   -- Debounce in ms after text change to trigger completion
    cursor_prediction = {
      enabled = true,            -- Show jump indicators after completions
      auto_advance = true,       -- When no changes, show cursor jump to last line
      dist_threshold = 2,        -- Min lines apart to show cursor jump (0 to disable)
    },
  },

  provider = {
    type = "autocomplete",             -- Provider: "autocomplete", "sweep", or "zeta"
    url = "http://localhost:8000",     -- URL of the provider server
    model = "autocomplete",            -- Model name
    temperature = 0.0,                 -- Sampling temperature
    max_tokens = 256,                  -- Max tokens to generate (autocomplete only)
    top_k = 50,                        -- Top-k sampling
    completion_timeout = 5000,         -- Timeout in ms for completion requests
    max_context_tokens = 512,          -- Max context tokens around cursor (0 = no limit)
    max_diff_history_tokens = 512,     -- Max tokens for diff history (0 = no limit)
  },

  debug = {
    immediate_shutdown = false,  -- Shutdown daemon immediately when no clients
  },
})
```

For detailed configuration documentation, see `:help cursortab-config`.

### Providers

The plugin supports three AI provider backends: Autocomplete, Sweep, and Zeta.

#### Autocomplete Provider (Default)

End-of-line completion using OpenAI-compatible API endpoints.

**Features:**

- End-of-line completion only
- No cursor jump predictions
- Stop at newline characters
- Works with any OpenAI-compatible `/v1/completions` endpoint

**Requirements:**

- An OpenAI-compatible completions endpoint

**Example Configuration:**

```lua
require("cursortab").setup({
  provider = {
    type = "autocomplete",
    url = "http://localhost:8000",
    model = "autocomplete",
  },
})
```

#### Zeta Provider

Local model support with OpenAI-compatible API format.

**Features:**

- End-of-line completion
- Local model execution
- OpenAI-compatible API

**Example Configuration:**

```lua
require("cursortab").setup({
  provider = {
    type = "zeta",
    url = "http://localhost:8000",
    model = "zeta",
  },
})
```

#### Sweep Provider

Sweep Next-Edit 1.5B model for fast, accurate next-edit predictions.

**Features:**

- Multi-line completions with token-based context (sends full file for small
  files, trimmed around cursor for large files)
- Fast inference (~500ms on CPU, <100ms on GPU)
- Outperforms larger models on next-edit accuracy
- Open-source weights available
- OpenAI-compatible API format

**Requirements:**

- vLLM or compatible inference server
- Sweep Next-Edit model downloaded from
  [Hugging Face](https://huggingface.co/sweepai/sweep-next-edit-1.5b)

**Example Configuration:**

```lua
require("cursortab").setup({
  provider = {
    type = "sweep",
    url = "http://localhost:8000",
    model = "sweep-next-edit-1.5b",
  },
})
```

**Setup Instructions:**

```bash
# Using llama.cpp (recommended)
# Download the GGUF model and run llama-server
llama-server -hf sweepai/sweep-next-edit-1.5b-GGUF --port 8000

# Or with a local GGUF file
llama-server -m sweep-next-edit-1.5b.q8_0.v2.gguf --port 8000
```

## Usage

- **Tab Key**: Navigate to cursor predictions or accept completions
- **Esc Key**: Reject current completions
- The plugin automatically shows jump indicators for predicted cursor positions
- Visual indicators appear for additions, deletions, and completions
- Off-screen jump targets show directional arrows with distance information

### Commands

- `:CursortabToggle`: Toggle the plugin on/off
- `:CursortabShowLog`: Show the cursortab log file in a new buffer
- `:CursortabClearLog`: Clear the cursortab log file
- `:CursortabStatus`: Show detailed status information about the plugin and
  daemon
- `:CursortabRestart`: Restart the cursortab daemon process

## Requirements

- Go 1.24.2+ (for building the server component)
- Neovim 0.8+ (for the plugin)

## Development

### Build

To build the server component:

```bash
cd server && go build
```

### Test

To run tests:

```bash
cd server && go test ./...
```

## FAQ

<details>
<summary>Which provider should I use?</summary>

- **Autocomplete**: End-of-line completions only. No multi-line or cursor
  prediction support.
- **Zeta** and **Sweep**: Both support multi-line completions and cursor
  predictions. Sweep generally produces better results.

For the best experience, use **Sweep** with the `sweep-next-edit-1.5b` model.

</details>

<details>
<summary>Why are completions slow?</summary>

1. Use a smaller or more quantized model (e.g., Q4 instead of Q8)
2. Decrease `provider.max_context_tokens` to send less context

</details>

<details>
<summary>Why are completions not working?</summary>

1. Update to the latest version and restart the daemon with `:CursortabRestart`
2. Increase `provider.completion_timeout` (default: 5000ms) to 10000 or more if your model is slow

</details>

<details>
<summary>How do I update the plugin?</summary>

Use your Neovim plugin manager to pull the latest changes, then run
`:CursortabRestart` to restart the daemon.

</details>

## Contributing

Contributions are welcome! Please open an issue or a pull request.

Feel free to open issues for bugs :)

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file
for details.
