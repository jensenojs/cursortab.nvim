-- Event handling and autocommands for cursortab.nvim

local buffer = require("cursortab.buffer")
local daemon = require("cursortab.daemon")
local ui = require("cursortab.ui")

---@class EventsModule
local events = {}

-- Track if events have been set up to prevent duplicate registrations
local events_setup_done = false

-- Skip exactly one TextChanged after accepting a completion via <Tab>
---@type boolean
local skip_next_text_changed = false

-- State for cursor movement suppression during completion application
---@type boolean
local skip_next_cursor_moved = false

-- Function to clear all visible completions and predictions
local function clear_all_completions()
	-- Clear cursor prediction UI
	ui.close_all()

	-- Send reject event to server
	daemon.send_reject()
end

-- Tab key handler
---@return string
local function on_tab()
	if ui.has_cursor_prediction() or ui.has_completion() then
		-- Suppress the immediate text change and cursor movement caused by applying the completion
		skip_next_text_changed = true
		skip_next_cursor_moved = true
		daemon.send_event("tab")
		return ""
	else
		return "\t"
	end
end

-- Escape key handler
---@return string
local function on_escape()
	daemon.send_event("esc")
	return "\27"
end

-- Set up all autocommands and keymaps
function events.setup()
	-- Prevent duplicate setup
	if events_setup_done then
		return
	end
	events_setup_done = true

	-- Track buffer/window focus changes to update cached state
	vim.api.nvim_create_autocmd({ "BufEnter", "WinEnter" }, {
		callback = vim.schedule_wrap(function()
			buffer.update_state()
		end),
	})

	-- Text change events
	vim.api.nvim_create_autocmd({ "TextChanged", "TextChangedI" }, {
		callback = function()
			-- Skip if buffer should be ignored
			if buffer.should_skip() then
				return
			end

			-- Skip exactly one text change immediately following a completion accept
			if skip_next_text_changed then
				skip_next_text_changed = false
				return
			end

			-- Handle cursor prediction (always clear - no partial match logic)
			if ui.has_cursor_prediction() then
				ui.ensure_close_all()
			elseif ui.has_completion() then
				-- For completions, check if typing matches the prediction
				-- If it matches, update ghost text locally to avoid visual glitch
				-- If it doesn't match, clear immediately to avoid showing stale ghost text
				local current_line = vim.api.nvim_get_current_line()
				local cursor_line = vim.fn.line(".")
				if ui.typing_matches_completion(cursor_line, current_line) then
					-- Update extmark position/content locally for smooth visual
					ui.update_ghost_text_for_typing(cursor_line, current_line)
				else
					ui.ensure_close_all()
				end
			end

			daemon.send_event("text_changed")
		end,
	})

	-- Cursor movement events
	vim.api.nvim_create_autocmd({ "CursorMoved" }, {
		callback = function()
			-- Skip if cursor movement events are temporarily suppressed (e.g., after tab key)
			if skip_next_cursor_moved then
				skip_next_cursor_moved = false
				return
			end

			if ui.has_cursor_prediction() or ui.has_completion() then
				ui.ensure_close_all()
			end
			daemon.send_event("cursor_moved_normal")
		end,
	})

	-- Insert mode events
	vim.api.nvim_create_autocmd({ "InsertEnter" }, {
		callback = function()
			daemon.send_event("insert_enter")
		end,
	})

	vim.api.nvim_create_autocmd({ "InsertLeave" }, {
		callback = function()
			-- Skip if buffer should be ignored
			if buffer.should_skip() then
				return
			end

			if ui.has_cursor_prediction() or ui.has_completion() then
				ui.ensure_close_all()
			end
			daemon.send_event("insert_leave")
		end,
	})

	-- Set up keymaps
	vim.keymap.set("i", "<Tab>", on_tab, { noremap = true, silent = true, expr = true })
	vim.keymap.set("n", "<Tab>", on_tab, { noremap = true, silent = true, expr = true })
	vim.keymap.set("n", "<Esc>", on_escape, { noremap = true, silent = true, expr = true })

	-- Set up autocommand to close completions/predictions on certain events
	vim.api.nvim_create_autocmd({ "ModeChanged", "CmdlineEnter", "CmdwinEnter", "BufEnter" }, {
		callback = function(args)
			-- Don't close when transitioning from normal to insert mode
			if args.event == "ModeChanged" and args.match and args.match:match("^n:i") then
				return
			end

			if ui.has_cursor_prediction() or ui.has_completion() then
				ui.ensure_close_all()
			end

			daemon.send_reject()
		end,
	})
end

-- Clear all completions (exposed for manual use)
function events.clear_all_completions()
	clear_all_completions()
end

return events
