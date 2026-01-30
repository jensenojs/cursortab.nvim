-- Main entry point for cursortab.nvim
-- Import all modules
local config = require("cursortab.config")
local daemon = require("cursortab.daemon")
local events = require("cursortab.events")
local ui = require("cursortab.ui")

---@class CursortabModule
local M = {}

-- RPC callback functions (called from Go daemon)
-- These must remain globally accessible for the RPC interface

---RPC callback: called when completion is rejected
function M.on_reject()
	ui.close_all()
end

---Accept current completion/prediction if available.
---@return boolean accepted
function M.accept()
	return events.accept()
end

---RPC callback: called when completion is ready
---@param diff_result DiffResult Completion diff result from Go daemon
function M.on_completion_ready(diff_result)
	ui.show_completion(diff_result)
end

---RPC callback: called when cursor prediction is ready
---@param line_num integer Predicted line number (1-indexed)
function M.on_cursor_prediction_ready(line_num)
	ui.show_cursor_prediction(line_num)
end

-- Public API functions for users

---Toggle cursortab functionality on/off
function M.toggle()
	local enabled = not daemon.is_enabled()
	daemon.set_enabled(enabled)

	if enabled then
		vim.notify("Cursortab enabled", vim.log.levels.INFO)
	else
		vim.notify("Cursortab disabled", vim.log.levels.INFO)
		-- Clear all completions and predictions when disabling
		events.clear_all_completions()
	end
end

---Show cursortab log file in a floating window
function M.show_log()
	local plugin_dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h:h:h")
	local log_path = plugin_dir .. "/server/cursortab.log"

	-- Check if log file exists
	if vim.fn.filereadable(log_path) == 0 then
		vim.notify("Log file not found: " .. log_path, vim.log.levels.WARN)
		return
	end

	-- Read the log file content
	local lines = vim.fn.readfile(log_path)

	-- Create scratch window using UI module
	ui.create_scratch_window("Cursortab Log", lines, {
		filetype = "log",
		move_to_end = true,
		size_mode = "fullscreen",
	})

	vim.notify("Showing cursortab log", vim.log.levels.INFO)
end

---Clear cursortab log file
function M.clear_log()
	local plugin_dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h:h:h")
	local log_path = plugin_dir .. "/server/cursortab.log"

	-- Check if log file exists
	if vim.fn.filereadable(log_path) == 0 then
		vim.notify("Log file not found: " .. log_path, vim.log.levels.WARN)
		return
	end

	-- Clear the log file by writing empty content
	vim.fn.writefile({}, log_path)

	vim.notify("Cursortab log cleared", vim.log.levels.INFO)
end

---Show cursortab status information in a floating window
function M.status()
	local daemon_status = daemon.check_daemon_status()
	local channel_status = daemon.get_channel_status()
	local plugin_enabled = daemon.is_enabled()

	-- Build status message
	local status_lines = {
		"  _____                      __       __ ",
		" / ___/_ _________ ___  ____/ /____ _/ / ",
		"/ /__/ // / __(_-</ _ \\/ __/ __/ _ `/ _ \\",
		"\\___/\\_,_/_/ /___/\\___/_/  \\__/\\_,_/_.__/",
		"",
		"Plugin State:",
		"  • Enabled: " .. (plugin_enabled and "✓ Yes" or "✗ No"),
		"",
		"Daemon Status:",
		"  • Socket exists: " .. (daemon_status.socket_exists and "✓ Yes" or "✗ No"),
		"  • PID file exists: " .. (daemon_status.pid_file_exists and "✓ Yes" or "✗ No"),
		"  • Process running: " .. (daemon_status.daemon_running and "✓ Yes" or "✗ No"),
	}

	if daemon_status.pid then
		table.insert(status_lines, "  • Process ID: " .. daemon_status.pid)
	end

	table.insert(status_lines, "")
	table.insert(status_lines, "Client Connection:")
	table.insert(status_lines, "  • Connected: " .. (channel_status.connected and "✓ Yes" or "✗ No"))

	if channel_status.channel_id then
		table.insert(status_lines, "  • Channel ID: " .. channel_status.channel_id)
	end

	-- Create scratch window using UI module
	ui.create_scratch_window("Cursortab Status", status_lines, {
		size_mode = "fit_content",
	})

	vim.notify("Cursortab status displayed", vim.log.levels.INFO)
end

---Restart cursortab daemon
function M.restart()
	vim.notify("Restarting cursortab daemon...", vim.log.levels.INFO)

	-- Clear any existing completions first
	events.clear_all_completions()

	-- Stop existing daemon (this now handles all cleanup reliably)
	local _, stop_message = daemon.stop_daemon()
	vim.notify(stop_message, vim.log.levels.INFO)

	-- Small delay to ensure cleanup is complete
	vim.defer_fn(function()
		-- Explicitly start the daemon
		local start_success = daemon.force_start()

		if start_success then
			vim.notify("Cursortab daemon restarted successfully", vim.log.levels.INFO)
		else
			vim.notify("Failed to start cursortab daemon", vim.log.levels.ERROR)
		end
	end, 200)
end

---Setup cursortab with user configuration
---@param user_config table|nil User configuration overrides
function M.setup(user_config)
	-- Setup configuration
	local cfg = config.setup(user_config)
	daemon.set_enabled(cfg.enabled)

	-- Create user commands
	vim.api.nvim_create_user_command("CursortabToggle", function()
		M.toggle()
	end, { desc = "Toggle Cursortab functionality" })

	vim.api.nvim_create_user_command("CursortabShowLog", function()
		M.show_log()
	end, { desc = "Show cursortab log file in a scratch window" })

	vim.api.nvim_create_user_command("CursortabClearLog", function()
		M.clear_log()
	end, { desc = "Clear cursortab log file" })

	vim.api.nvim_create_user_command("CursortabStatus", function()
		M.status()
	end, { desc = "Show cursortab status information" })

	vim.api.nvim_create_user_command("CursortabRestart", function()
		M.restart()
	end, { desc = "Restart cursortab daemon" })

	-- Setup highlight groups
	config.setup_highlights()

	-- Set up highlight namespace
	vim.api.nvim_set_hl_ns(daemon.get_namespace_id())

	-- Setup events and autocommands
	events.setup()
end

-- Auto-initialize with default settings
M.setup()

return M
