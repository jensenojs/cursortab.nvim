-- Configuration management for cursortab.nvim

---@class CursortabUIColorsConfig
---@field deletion string
---@field addition string
---@field modification string
---@field completion string

---@class CursortabUIJumpConfig
---@field symbol string
---@field text string
---@field show_distance boolean
---@field bg_color string
---@field fg_color string

---@class CursortabUIConfig
---@field colors CursortabUIColorsConfig
---@field jump CursortabUIJumpConfig

---@class CursortabCursorPredictionConfig
---@field enabled boolean
---@field auto_advance boolean
---@field dist_threshold integer

---@class CursortabBehaviorConfig
---@field idle_completion_delay integer
---@field text_change_debounce integer
---@field cursor_prediction CursortabCursorPredictionConfig

---@class CursortabProviderConfig
---@field type string
---@field url string
---@field model string
---@field temperature number
---@field max_tokens integer
---@field top_k integer
---@field completion_timeout integer
---@field max_context_tokens integer
---@field max_diff_history_tokens integer

---@class CursortabDebugConfig
---@field immediate_shutdown boolean

---@class CursortabConfig
---@field enabled boolean
---@field log_level string
---@field ui CursortabUIConfig
---@field behavior CursortabBehaviorConfig
---@field provider CursortabProviderConfig
---@field debug CursortabDebugConfig

-- Default configuration
---@type CursortabConfig
local default_config = {
	enabled = true,
	log_level = "info",

	ui = {
		colors = {
			deletion = "#4f2f2f",
			addition = "#394f2f",
			modification = "#282e38",
			completion = "#80899c",
		},
		jump = {
			symbol = "",
			text = " TAB ",
			show_distance = true,
			bg_color = "#373b45",
			fg_color = "#bac1d1",
		},
	},

	behavior = {
		idle_completion_delay = 50, -- Delay in ms after being idle in normal mode to trigger completion (-1 to disable)
		text_change_debounce = 50, -- Debounce in ms after text changed to trigger completion
		cursor_prediction = {
			enabled = true, -- Show jump indicators after completions
			auto_advance = true, -- When completion has no changes, show cursor jump to last line
			dist_threshold = 2, -- Min lines apart to show cursor jump between completions (0 to disable)
		},
	},

	provider = {
		type = "autocomplete", -- "autocomplete", "sweep", or "zeta"
		url = "http://localhost:8000", -- URL of the provider server
		model = "autocomplete", -- Model name
		temperature = 0.0, -- Sampling temperature
		max_tokens = 256, -- Max tokens to generate (autocomplete only)
		top_k = 50, -- Top-k sampling
		completion_timeout = 5000, -- Timeout in ms for completion requests
		max_context_tokens = 512, -- Max context tokens to include around cursor (0 = no limit)
		max_diff_history_tokens = 512, -- Max tokens for diff history (0 = no limit)
	},

	debug = {
		immediate_shutdown = false, -- Shutdown daemon immediately when no clients are connected
	},
}

-- Deprecated field mappings (old flat field -> new nested path)
-- A nil value means the option was removed entirely
local deprecated_mappings = {
	-- UI colors (old -> new)
	deletion_color = { "ui", "colors", "deletion" },
	addition_color = { "ui", "colors", "addition" },
	modification_color = { "ui", "colors", "modification" },
	completion_color = { "ui", "colors", "completion" },
	-- UI jump (old -> new)
	jump_symbol = { "ui", "jump", "symbol" },
	jump_text = { "ui", "jump", "text" },
	jump_show_distance = { "ui", "jump", "show_distance" },
	jump_bg_color = { "ui", "jump", "bg_color" },
	jump_fg_color = { "ui", "jump", "fg_color" },
	-- Behavior (old -> new)
	idle_completion_delay = { "behavior", "idle_completion_delay" },
	text_changed_debounce = { "behavior", "text_change_debounce" },
	completion_timeout = { "provider", "completion_timeout" },
	cursor_prediction = { "behavior", "cursor_prediction" },
	-- Provider (old -> new)
	provider = { "provider", "type" },
	provider_url = { "provider", "url" },
	provider_model = { "provider", "model" },
	provider_temperature = { "provider", "temperature" },
	provider_max_tokens = { "provider", "max_tokens" },
	provider_top_k = { "provider", "top_k" },
	max_context_tokens = { "provider", "max_context_tokens" },
	max_diff_history_tokens = { "provider", "max_diff_history_tokens" },
	-- Debug (old -> new)
	debug_immediate_shutdown = { "debug", "immediate_shutdown" },
	-- Removed options (nil = no migration, just warn)
	event_debounce = nil,
	debug_color = nil,
}

-- Migrate deprecated flat config to new nested structure
---@param user_config table
---@return table
local function migrate_deprecated_config(user_config)
	local migrated = vim.deepcopy(user_config)
	local deprecated_keys = {}
	local removed_keys = {}

	for old_key, new_path in pairs(deprecated_mappings) do
		if migrated[old_key] ~= nil then
			-- Skip if key exists in new format (e.g., provider = { type = ... } is new, provider = "autocomplete" is old)
			-- When new_path[1] == old_key, the new format uses a table at that key
			if new_path and new_path[1] == old_key and type(migrated[old_key]) == "table" then
				goto continue
			end

			if new_path == nil then
				-- Option was removed entirely
				table.insert(removed_keys, old_key)
			else
				-- Option was moved to new location
				table.insert(deprecated_keys, old_key)
				-- Navigate to the nested location and set the value
				local target = migrated
				for i = 1, #new_path - 1 do
					local key = new_path[i]
					-- Create table if nil or if it's not a table (e.g., old "provider" string)
					if target[key] == nil or type(target[key]) ~= "table" then
						target[key] = {}
					end
					target = target[key]
				end
				target[new_path[#new_path]] = migrated[old_key]
			end
			migrated[old_key] = nil

			::continue::
		end
	end

	if #deprecated_keys > 0 then
		vim.schedule(function()
			vim.notify(
				"[cursortab.nvim] Deprecated config keys detected: "
					.. table.concat(deprecated_keys, ", ")
					.. "\nPlease migrate to the new nested structure. See :help cursortab-config",
				vim.log.levels.WARN
			)
		end)
	end

	if #removed_keys > 0 then
		vim.schedule(function()
			vim.notify(
				"[cursortab.nvim] Removed config keys detected: "
					.. table.concat(removed_keys, ", ")
					.. "\nThese options no longer have any effect.",
				vim.log.levels.WARN
			)
		end)
	end

	return migrated
end

-- Valid values for enum-like config options
local valid_provider_types = { autocomplete = true, sweep = true, zeta = true }
local valid_log_levels = { debug = true, info = true, warn = true, error = true }

-- Validate configuration values
---@param cfg table
local function validate_config(cfg)
	-- Validate provider type
	if cfg.provider and cfg.provider.type then
		if not valid_provider_types[cfg.provider.type] then
			error(string.format(
				"[cursortab.nvim] Invalid provider.type '%s'. Must be one of: autocomplete, sweep, zeta",
				cfg.provider.type
			))
		end
	end

	-- Validate log level
	if cfg.log_level and not valid_log_levels[cfg.log_level] then
		error(string.format(
			"[cursortab.nvim] Invalid log_level '%s'. Must be one of: debug, info, warn, error",
			cfg.log_level
		))
	end

	-- Validate numeric ranges
	if cfg.behavior then
		if cfg.behavior.idle_completion_delay and cfg.behavior.idle_completion_delay < -1 then
			error("[cursortab.nvim] behavior.idle_completion_delay must be >= -1")
		end
		if cfg.behavior.text_change_debounce and cfg.behavior.text_change_debounce < 0 then
			error("[cursortab.nvim] behavior.text_change_debounce must be >= 0")
		end
	end

	if cfg.provider then
		if cfg.provider.max_tokens and cfg.provider.max_tokens < 0 then
			error("[cursortab.nvim] provider.max_tokens must be >= 0")
		end
		if cfg.provider.completion_timeout and cfg.provider.completion_timeout < 0 then
			error("[cursortab.nvim] provider.completion_timeout must be >= 0")
		end
		if cfg.provider.max_context_tokens and cfg.provider.max_context_tokens < 0 then
			error("[cursortab.nvim] provider.max_context_tokens must be >= 0")
		end
		if cfg.provider.max_diff_history_tokens and cfg.provider.max_diff_history_tokens < 0 then
			error("[cursortab.nvim] provider.max_diff_history_tokens must be >= 0")
		end
	end
end

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
	local migrated = migrate_deprecated_config(user_config or {})
	validate_config(migrated)
	current_config = vim.tbl_deep_extend("force", vim.deepcopy(default_config), migrated)
	return current_config
end

-- Set up highlight groups based on current configuration
function config.setup_highlights()
	---@type CursortabConfig
	local cfg = current_config

	vim.api.nvim_set_hl(0, "cursortabhl_deletion", {
		ctermbg = "DarkRed",
		bg = cfg.ui.colors.deletion,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_addition", {
		ctermbg = "DarkGreen",
		bg = cfg.ui.colors.addition,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_modification", {
		ctermbg = "DarkGray",
		bg = cfg.ui.colors.modification,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_completion", {
		ctermfg = "DarkBlue",
		fg = cfg.ui.colors.completion,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_jump_symbol", {
		ctermfg = "Cyan",
		fg = cfg.ui.jump.bg_color,
		bold = false,
	})

	vim.api.nvim_set_hl(0, "cursortabhl_jump_text", {
		ctermbg = "Cyan",
		ctermfg = "Black",
		bg = cfg.ui.jump.bg_color,
		fg = cfg.ui.jump.fg_color,
		bold = false,
	})
end

return config
