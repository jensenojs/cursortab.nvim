-- Daemon management and RPC communication for cursortab.nvim

local config = require("cursortab.config")
local buffer = require("cursortab.buffer")

local daemon = {}

-- Module state
---@type integer|nil
local chan = nil
local ns_id = vim.api.nvim_create_namespace("cursortab")
local event_debounce_timer = nil
local is_enabled = true

-- Start the daemon process
local function start_daemon()
	local plugin_dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h:h:h")
	local binary_name = "cursortab"
	if vim.fn.has("win32") == 1 or vim.fn.has("win64") == 1 then
		binary_name = binary_name .. ".exe"
	end
	local binary_path = plugin_dir .. "/server/" .. binary_name
	local socket_path = plugin_dir .. "/server/cursortab.sock"

	-- Check if binary exists
	if vim.fn.executable(binary_path) == 0 then
		vim.notify(
			"cursortab binary not found at: "
				.. binary_path
				.. "\n"
				.. "Please ensure the Go server was built during installation.\n"
				.. "If using lazy.nvim, make sure the build step is configured:\n"
				.. 'build = "cd server && go build"',
			vim.log.levels.ERROR
		)
		return false
	end

	-- Create JSON configuration
	local cfg = config.get()
	local json_config = vim.json.encode({
		ns_id = ns_id,
		provider = cfg.provider,
		idle_completion_delay = cfg.idle_completion_delay,
		text_change_debounce = cfg.text_changed_debounce,
		completion_timeout = cfg.completion_timeout,
		debug_immediate_shutdown = cfg.debug_immediate_shutdown,
		max_context_tokens = cfg.max_context_tokens,
		provider_url = cfg.provider_url,
		provider_model = cfg.provider_model,
		provider_temperature = cfg.provider_temperature,
		provider_max_tokens = cfg.provider_max_tokens,
		provider_top_k = cfg.provider_top_k,
		log_level = cfg.log_level,
		cursor_prediction = {
			enabled = cfg.cursor_prediction.enabled,
			auto_advance = cfg.cursor_prediction.auto_advance,
			dist_threshold = cfg.cursor_prediction.dist_threshold,
		},
	})

	local env = vim.fn.environ()
	env.CURSORTAB_CONFIG = json_config

	-- Start daemon if socket doesn't exist
	if vim.fn.filereadable(socket_path) == 0 then
		vim.fn.jobstart({ binary_path, "--daemon" }, {
			env = env,
			detach = true,
		})

		-- Wait for socket
		for _ = 1, 50 do
			vim.wait(100)
			if vim.fn.filereadable(socket_path) == 1 then
				break
			end
		end
	end

	-- Connect to daemon
	chan = vim.fn.jobstart({ binary_path }, {
		rpc = true,
		env = env,
	})

	return chan > 0
end

-- Send RPC event to daemon
---@param event_name string
local function send_rpc_event(event_name)
	if buffer.should_skip() or not is_enabled then
		return
	end

	-- Ensure we have a valid channel
	if not chan or chan <= 0 then
		if not start_daemon() then
			return
		end
	end

	-- Use pcall with timeout for better error handling
	local success = pcall(function()
		-- Ensure chan is valid before sending
		if chan and chan > 0 then
			-- Send the event with minimal overhead
			vim.fn.rpcnotify(chan, "cursortab_event", event_name)
		end
	end)

	if not success then
		chan = nil
	end
end

-- Public API

-- Send event with debouncing
---@param event_name string
function daemon.send_event(event_name)
	if event_debounce_timer then
		vim.fn.timer_stop(event_debounce_timer)
	end
	local cfg = config.get()
	event_debounce_timer = vim.fn.timer_start(cfg.event_debounce, function()
		send_rpc_event(event_name)
	end)
end

-- Get the namespace ID
function daemon.get_namespace_id()
	return ns_id
end

-- Enable/disable daemon functionality
---@param enabled boolean
function daemon.set_enabled(enabled)
	is_enabled = enabled
end

function daemon.is_enabled()
	return is_enabled
end

-- Send reject event directly (for clearing completions)
function daemon.send_reject()
	if chan and chan > 0 then
		pcall(function()
			vim.fn.rpcnotify(chan, "cursortab_event", "esc")
		end)
	end
end

-- Check daemon process status
function daemon.check_daemon_status()
	local plugin_dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h:h:h")
	local socket_path = plugin_dir .. "/server/cursortab.sock"
	local pid_path = plugin_dir .. "/server/cursortab.pid"

	local status = {
		socket_exists = vim.fn.filereadable(socket_path) == 1,
		pid_file_exists = vim.fn.filereadable(pid_path) == 1,
		daemon_running = false,
		pid = nil,
	}

	-- Check if PID file exists and process is running
	if status.pid_file_exists then
		local pid_content = vim.fn.readfile(pid_path)
		if #pid_content > 0 then
			local pid = tonumber(pid_content[1])
			if pid then
				status.pid = pid
				-- Check if process exists (works on Unix systems)
				vim.fn.system("kill -0 " .. pid .. " 2>/dev/null")
				status.daemon_running = vim.v.shell_error == 0
			end
		end
	end

	return status
end

-- Get channel status
function daemon.get_channel_status()
	return {
		connected = chan and chan > 0,
		channel_id = chan,
	}
end

-- Stop daemon process
function daemon.stop_daemon()
	local plugin_dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":h:h:h")
	local pid_path = plugin_dir .. "/server/cursortab.pid"

	if vim.fn.filereadable(pid_path) == 0 then
		return false, "PID file not found"
	end

	local pid_content = vim.fn.readfile(pid_path)
	if #pid_content == 0 then
		return false, "PID file is empty"
	end

	local pid = tonumber(pid_content[1])
	if not pid then
		return false, "Invalid PID in file"
	end

	-- Send TERM signal to daemon
	vim.fn.system("kill " .. pid .. " 2>/dev/null")
	local success = vim.v.shell_error == 0

	if success then
		-- Reset channel
		chan = nil

		-- Wait for socket to be removed (daemon cleanup)
		local socket_path = plugin_dir .. "/server/cursortab.sock"
		for _ = 1, 50 do
			vim.wait(100)
			if vim.fn.filereadable(socket_path) == 0 then
				break
			end
		end

		return true, "Daemon stopped successfully"
	else
		return false, "Failed to stop daemon (process may not exist)"
	end
end

return daemon
