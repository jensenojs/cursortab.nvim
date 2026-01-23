-- Configuration management for cursortab.nvim

---@class CursortabCursorPredictionConfig
---@field enabled boolean
---@field auto_advance boolean
---@field dist_threshold integer

---@class CursortabConfig
---@field deletion_color string
---@field addition_color string
---@field modification_color string
---@field completion_color string
---@field jump_symbol string
---@field jump_text string
---@field jump_show_distance boolean
---@field jump_bg_color string
---@field jump_fg_color string
---@field enabled boolean
---@field provider string
---@field idle_completion_delay integer
---@field text_changed_debounce integer
---@field event_debounce integer
---@field completion_timeout integer
---@field cursor_prediction CursortabCursorPredictionConfig
---@field debug_immediate_shutdown boolean
---@field debug_color string
---@field provider_url string
---@field provider_model string
---@field provider_temperature number
---@field provider_max_tokens integer
---@field provider_top_k integer
---@field max_context_tokens integer
---@field log_level string

-- Default configuration
---@type CursortabConfig
local default_config = {
	-- CUSTOMIZATION
	deletion_color = "#4f2f2f",
	addition_color = "#394f2f",
	modification_color = "#282e38",
	completion_color = "#80899c",
	jump_symbol = "",
	jump_text = " TAB ", -- Text after jump symbol
	jump_show_distance = true, -- Show line distance for off-screen
	jump_bg_color = "#373b45", -- Jump background color
	jump_fg_color = "#bac1d1", -- Jump foreground color

	-- OPTIONS
	enabled = true, -- Whether the plugin is enabled
	provider = "autocomplete", -- "autocomplete", "sweep", or "zeta"
	idle_completion_delay = 50, -- Delay in ms after being idle in normal mode to trigger completion (-1 to disable)
	text_changed_debounce = 50, -- Debounce in ms after text changed to trigger completion
	completion_timeout = 5000, -- Timeout in ms for completion requests
	cursor_prediction = {
		enabled = true, -- Show jump indicators after completions
		auto_advance = true, -- On no-op (no changes), jump to last line and retrigger
		dist_threshold = 2, -- Lines apart to trigger staging (0 to disable)
	},

	-- Provider Options (applied to all providers: autocomplete, sweep, zeta)
	provider_url = "http://localhost:8000", -- URL of the provider server
	provider_model = "autocomplete", -- Model name (e.g., "autocomplete", "sweep-next-edit-1.5b", "zeta")
	provider_temperature = 0.0, -- Sampling temperature
	provider_max_tokens = 256, -- Max tokens to generate
	provider_top_k = 50, -- Top-k sampling (used by some providers)

	-- Context Options
	max_context_tokens = 1024, -- Max tokens to send as context (0 = no limit)

	-- INTERNAL
	log_level = "info", -- Log level: "debug", "info", "warn", "error"
	event_debounce = 10, -- Debounce in ms for events to go
	debug_immediate_shutdown = false, -- Shutdown daemon immediately when no clients are connected
	debug_color = "#cccc55",
}

---@class ConfigModule
local config = {}
---@type CursortabConfig
local current_config = vim.deepcopy(default_config)

-- Get current configuration
---@return CursortabConfig
function config.get()
	return current_config
end

-- Set up configuration with user overrides
---@param user_config table|nil User configuration overrides
---@return CursortabConfig
function config.setup(user_config)
	current_config = vim.tbl_deep_extend("force", vim.deepcopy(default_config), user_config or {})
	return current_config
end

-- Set up highlight groups based on current configuration
function config.setup_highlights()
	---@type CursortabConfig
	local cfg = current_config

	vim.api.nvim_set_hl(0, "cursortabhl_deletion", {
		ctermbg = "DarkRed",
		bg = cfg.deletion_color,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_addition", {
		ctermbg = "DarkGreen",
		bg = cfg.addition_color,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_modification", {
		ctermbg = "DarkGray",
		bg = cfg.modification_color,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_completion", {
		ctermfg = "DarkBlue",
		fg = cfg.completion_color,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_debug", {
		ctermfg = "DarkYellow",
		fg = cfg.debug_color,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_jump_symbol", {
		ctermfg = "Cyan",
		fg = cfg.jump_bg_color,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_jump_text", {
		ctermbg = "Cyan",
		ctermfg = "Black",
		bg = cfg.jump_bg_color,
		fg = cfg.jump_fg_color,
		bold = false,
	})
end

return config
