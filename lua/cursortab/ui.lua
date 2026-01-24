-- UI management for completion and cursor prediction visualization

local config = require("cursortab.config")
local daemon = require("cursortab.daemon")

---@class UIModule
local ui = {}

-- UI state
---@type boolean
local has_completion = false
---@type boolean
local has_cursor_prediction = false

-- Expected line state for partial typing optimization (append_chars only)
---@type string|nil
local expected_line = nil -- Target line content
---@type integer|nil
local expected_line_num = nil -- Which line (1-indexed)
---@type integer|nil
local original_len = nil -- Original content length before ghost text
---@type integer|nil
local append_chars_extmark_id = nil -- Extmark ID for the append_chars ghost text
---@type integer|nil
local append_chars_buf = nil -- Buffer where the extmark was created

-- State for cursor prediction jump text
---@type integer|nil
local jump_text_extmark_id = nil
---@type integer|nil
local jump_text_buf = nil
---@type integer|nil
local absolute_jump_win = nil
---@type integer|nil
local absolute_jump_buf = nil

---@class ExtmarkInfo
---@field buf integer
---@field extmark_id integer

---@class WindowInfo
---@field win_id integer
---@field buf_id integer

-- State for completion diff visualization
---@type ExtmarkInfo[]
local completion_extmarks = {} -- Array of {buf, extmark_id} for cleanup
---@type WindowInfo[]
local completion_windows = {} -- Array of {win_id, buf_id} for overlay window cleanup

---@class LineDiff
---@field type string Diff type: "deletion", "addition", "modification", "append_chars", "delete_chars", "replace_chars", "modification_group", "addition_group"
---@field lineNumber integer Line number (1-indexed)
---@field content string New content
---@field oldContent string Old content (for modifications)
---@field colStart integer Start column (0-based) for character-level changes
---@field colEnd integer End column (0-based) for character-level changes
---@field startLine integer|nil For group types: starting line number of the group (1-indexed)
---@field endLine integer|nil For group types: ending line number of the group (1-indexed)
---@field maxOffset integer|nil For modification groups: maximum left offset for positioning
---@field groupLines string[]|nil For group types: array of content lines in the group

---@class DiffResult
---@field changes table<string, LineDiff> Map of line number (string) to diff operation
---@field isOnlyLineDeletion boolean True if the diff contains only deletions
---@field lastDeletion integer The line number (1-indexed) of the last deletion, -1 if no deletion
---@field lastAddition integer The line number (1-indexed) of the last addition, -1 if no addition
---@field lastLineModification integer The line number (1-indexed) of the last line modification, -1 if no line modification
---@field lastAppendChars integer The line number (1-indexed) of the last append chars, -1 if no append chars
---@field lastDeleteChars integer The line number (1-indexed) of the last delete chars, -1 if no delete chars
---@field lastReplaceChars integer The line number (1-indexed) of the last replace chars, -1 if no replace chars
---@field cursorLine integer The optimal line (1-indexed) to position cursor, -1 if no positioning needed
---@field cursorCol integer The optimal column (0-indexed) to position cursor, -1 if no positioning needed
---@field startLine integer Start line of the extracted range (1-indexed)
---@field endLineInclusive integer End line of the extracted range (1-indexed, inclusive)

-- Helper function to close cursor prediction jump text
local function ensure_close_cursor_prediction()
	-- Clear jump text extmark
	if jump_text_extmark_id and jump_text_buf and vim.api.nvim_buf_is_valid(jump_text_buf) then
		vim.api.nvim_buf_del_extmark(jump_text_buf, daemon.get_namespace_id(), jump_text_extmark_id)
		jump_text_extmark_id = nil
		jump_text_buf = nil
	end

	-- Close absolute positioning window if it exists
	if absolute_jump_win and vim.api.nvim_win_is_valid(absolute_jump_win) then
		vim.api.nvim_win_close(absolute_jump_win, true)
		absolute_jump_win = nil
	end

	-- Clean up absolute jump buffer
	if absolute_jump_buf and vim.api.nvim_buf_is_valid(absolute_jump_buf) then
		vim.api.nvim_buf_delete(absolute_jump_buf, { force = true })
		absolute_jump_buf = nil
	end
end

-- Function to close completion diff highlighting
local function ensure_close_completion()
	-- Clear all completion extmarks
	for _, extmark_info in ipairs(completion_extmarks) do
		if extmark_info.buf and vim.api.nvim_buf_is_valid(extmark_info.buf) then
			pcall(function()
				vim.api.nvim_buf_del_extmark(extmark_info.buf, daemon.get_namespace_id(), extmark_info.extmark_id)
			end)
		end
	end

	-- Close all overlay windows
	for _, window_info in ipairs(completion_windows) do
		if window_info.win_id and vim.api.nvim_win_is_valid(window_info.win_id) then
			pcall(function()
				vim.api.nvim_win_close(window_info.win_id, true)
			end)
		end
		if window_info.buf_id and vim.api.nvim_buf_is_valid(window_info.buf_id) then
			pcall(function()
				vim.api.nvim_buf_delete(window_info.buf_id, { force = true })
			end)
		end
	end

	-- Reset state
	completion_extmarks = {}
	completion_windows = {}
end

-- Get the editor column offset (signs, number col, etc.)
---@param win integer
---@return integer
local function get_editor_col_offset(win)
	---@type table[]
	local wininfo = vim.fn.getwininfo(win)
	if #wininfo > 0 then
		return wininfo[1].textoff or 0
	end
	return 0
end

-- Trim a string by a given number of display columns from the left
---@param text string
---@param display_cols integer
---@return string trimmed_text, integer bytes_trimmed, integer chars_trimmed
local function trim_left_display_cols(text, display_cols)
	if not text or text == "" or display_cols <= 0 then
		return text, 0, 0
	end

	local total_chars = vim.fn.strchars(text)
	local trimmed_chars = 0
	local accumulated_width = 0

	-- Incrementally consume characters until we've trimmed the requested display width
	while trimmed_chars < total_chars and accumulated_width < display_cols do
		local ch = vim.fn.strcharpart(text, trimmed_chars, 1)
		local ch_width = vim.fn.strdisplaywidth(ch)
		accumulated_width = accumulated_width + ch_width
		trimmed_chars = trimmed_chars + 1
	end

	-- Compute bytes trimmed corresponding to the number of characters trimmed
	local bytes_trimmed = vim.str_byteindex(text, "utf-8", trimmed_chars)
	local trimmed_text = vim.fn.strcharpart(text, trimmed_chars)

	return trimmed_text, bytes_trimmed, trimmed_chars
end

-- Create transparent overlay window with syntax highlighting
---@param parent_win integer
---@param buffer_line integer
---@param col integer
---@param content string|string[]
---@param syntax_ft string|nil
---@param bg_highlight string|nil
---@param min_width integer|nil
---@return integer, integer, integer # overlay_win, overlay_buf, bytes_trimmed_first_line
local function create_overlay_window(parent_win, buffer_line, col, content, syntax_ft, bg_highlight, min_width)
	-- Create buffer for overlay content
	---@type integer
	local overlay_buf = vim.api.nvim_create_buf(false, true)

	-- Set buffer content
	---@type string[]
	local content_lines = type(content) == "table" and content or { content }

	-- Determine horizontal scroll (leftmost visible text column) for the parent window
	---@type integer
	local leftcol = vim.api.nvim_win_call(parent_win, function()
		local view = vim.fn.winsaveview()
		return view.leftcol or 0
	end)

	-- Compute how many display columns of the overlay content are scrolled off to the left
	---@type integer
	local trim_cols = math.max(0, leftcol - col)

	-- If needed, trim the left side of each line by trim_cols display columns
	---@type integer
	local bytes_trimmed_first_line = 0
	if trim_cols > 0 then
		for i, line_content in ipairs(content_lines) do
			local trimmed, bytes_trimmed = trim_left_display_cols(line_content or "", trim_cols)
			content_lines[i] = trimmed
			if i == 1 then
				bytes_trimmed_first_line = bytes_trimmed
			end
		end
	end

	vim.api.nvim_buf_set_lines(overlay_buf, 0, -1, false, content_lines)

	-- Set filetype for syntax highlighting if provided
	if syntax_ft and syntax_ft ~= "" then
		vim.api.nvim_set_option_value("filetype", syntax_ft, { buf = overlay_buf })
	end

	-- Make buffer non-modifiable
	vim.api.nvim_set_option_value("modifiable", false, { buf = overlay_buf })

	-- Calculate window dimensions
	---@type integer
	local max_width = 0
	for _, line_content in ipairs(content_lines) do
		max_width = math.max(max_width, vim.fn.strdisplaywidth(line_content))
	end

	-- Use minimum width if specified (useful for covering original content)
	if min_width and min_width > max_width then
		-- Account for scrolled-off columns when enforcing a minimum overlay width
		local adjusted_min_width = math.max(0, min_width - trim_cols)
		max_width = math.max(max_width, adjusted_min_width)
	end

	-- Get editor offsets
	---@type integer
	local left_offset = get_editor_col_offset(parent_win)

	-- Convert absolute buffer line to window-relative line
	-- buffer_line is 0-based, but we need window-relative positioning
	---@type integer
	local first_visible_line = vim.api.nvim_win_call(parent_win, function()
		return vim.fn.line("w0")
	end)
	---@type integer
	local window_relative_line = buffer_line - (first_visible_line - 1)

	-- Create floating window
	---@type integer
	local overlay_win = vim.api.nvim_open_win(overlay_buf, false, {
		relative = "win",
		win = parent_win,
		row = window_relative_line,
		-- Position horizontally relative to the visible text start (leftcol)
		col = left_offset + math.max(0, col - leftcol),
		width = max_width,
		height = #content_lines,
		style = "minimal",
		zindex = 1,
		focusable = false,
	})

	-- Set background highlighting to match main window
	if bg_highlight and bg_highlight ~= "" then
		vim.api.nvim_set_option_value("winhighlight", "Normal:" .. bg_highlight, { win = overlay_win })
	else
		-- Check if overlay is on cursor line and cursorline is enabled
		---@type integer
		local current_line = vim.api.nvim_win_call(parent_win, function()
			return vim.fn.line(".")
		end)
		---@type boolean
		local cursorline_enabled = vim.api.nvim_win_call(parent_win, function()
			return vim.wo.cursorline
		end)

		-- Always ensure no transparency
		vim.api.nvim_set_option_value("winblend", 0, { win = overlay_win })

		-- Use CursorLine highlight if overlay is on cursor line and cursorline is active
		if cursorline_enabled and (buffer_line + 1) == current_line then
			vim.api.nvim_set_option_value("winhighlight", "Normal:CursorLine", { win = overlay_win })
		else
			vim.api.nvim_set_option_value("winhighlight", "Normal:Normal", { win = overlay_win })
		end
	end

	return overlay_win, overlay_buf, bytes_trimmed_first_line
end

-- Helper to clear expected line state
local function clear_expected_line_state()
	expected_line = nil
	expected_line_num = nil
	original_len = nil
	append_chars_extmark_id = nil
	append_chars_buf = nil
end

-- Function to show completion diff highlighting (called from Go)
---@param diff_result DiffResult Completion diff result from Go daemon
local function show_completion(diff_result)
	-- Clear expected line state at start (will be populated if we find append_chars)
	clear_expected_line_state()

	-- Get current buffer
	---@type integer
	local current_buf = vim.api.nvim_get_current_buf()

	-- Don't show in floating windows
	---@type integer
	local current_win = vim.api.nvim_get_current_win()
	---@type table
	local win_config = vim.api.nvim_win_get_config(current_win)
	if win_config.relative ~= "" then
		return
	end

	local addition_offset = 0

	-- Collect and sort line numbers to process changes in order
	-- This is critical because addition_offset must be accumulated in ascending line order
	local sorted_lines = {}
	for line_str, _ in pairs(diff_result.changes or {}) do
		local line_num = tonumber(line_str)
		if line_num then
			table.insert(sorted_lines, line_num)
		end
	end
	table.sort(sorted_lines)

	-- Process each change in sorted line order
	for _, sorted_line_num in ipairs(sorted_lines) do
		local line_str = tostring(sorted_line_num)
		local change = diff_result.changes[line_str]
		---@type LineDiff
		local line_diff = change
		if sorted_line_num > 0 then
			-- Convert from relative line number to absolute buffer line number
			-- diff_result line numbers are relative to the extracted range (startLine to endLineInclusive)
			---@type integer
			local absolute_line_num = (diff_result.startLine or 1) + sorted_line_num - 1
			-- Convert to 0-based line number for nvim API
			---@type integer
			local nvim_line = absolute_line_num - 1

			-- Handle different diff types
			if line_diff.type == "append_chars" then
				-- For append_chars, show only the appended part using colStart
				local appended_text = string.sub(line_diff.content, line_diff.colStart + 1)

				-- Store expected line state for partial typing optimization
				-- Only store the first append_chars (most relevant for typing)
				local is_first_append_chars = expected_line == nil
				if is_first_append_chars then
					expected_line = line_diff.content
					expected_line_num = absolute_line_num
					original_len = line_diff.colStart
				end

				if appended_text and appended_text ~= "" then
					-- Try multiple positioning strategies to ensure visibility
					local line_content = vim.api.nvim_buf_get_lines(current_buf, nvim_line, nvim_line + 1, false)[1]
						or ""
					local line_length = #line_content

					-- Position virtual text at the end of existing content or at colStart, whichever is valid
					local virt_col = math.min(line_diff.colStart, line_length)

					local extmark_id =
						vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), nvim_line, virt_col, {
							virt_text = { { appended_text, "cursortabhl_completion" } },
							virt_text_pos = "overlay",
							hl_mode = "combine",
						})
					table.insert(completion_extmarks, { buf = current_buf, extmark_id = extmark_id })

					-- Store extmark info for the first append_chars (for live update during typing)
					if is_first_append_chars then
						append_chars_extmark_id = extmark_id
						append_chars_buf = current_buf
					end
				end
			elseif line_diff.type == "delete_chars" then
				-- For delete_chars, highlight the column range that was deleted
				local line_content = vim.api.nvim_buf_get_lines(current_buf, nvim_line, nvim_line + 1, false)[1] or ""
				local line_length = #line_content

				-- Ensure column bounds are valid
				local col_start = math.max(0, math.min(line_diff.colStart, line_length))
				local col_end = math.max(col_start, math.min(line_diff.colEnd, line_length))

				if col_end > col_start then
					local extmark_id =
						vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), nvim_line, col_start, {
							end_col = col_end,
							hl_group = "cursortabhl_deletion",
							hl_mode = "combine",
						})
					table.insert(completion_extmarks, { buf = current_buf, extmark_id = extmark_id })
				end
			elseif line_diff.type == "deletion" then
				-- For deletion, highlight the entire line
				local line_content = vim.api.nvim_buf_get_lines(current_buf, nvim_line, nvim_line + 1, false)[1] or ""

				if line_content ~= "" then
					local extmark_id =
						vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), nvim_line, 0, {
							end_col = #line_content,
							hl_group = "cursortabhl_deletion",
							hl_mode = "combine",
						})
					table.insert(completion_extmarks, { buf = current_buf, extmark_id = extmark_id })
				else
					-- For empty lines, create a virtual text indicator
					local extmark_id =
						vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), nvim_line, 0, {
							virt_text = { { "~", "cursortabhl_deletion" } },
							virt_text_pos = "overlay",
							hl_mode = "combine",
						})
					table.insert(completion_extmarks, { buf = current_buf, extmark_id = extmark_id })
				end
			elseif line_diff.type == "replace_chars" then
				-- Phase 2: replace_chars - Overlay entire line with minimum width to cover original content
				local syntax_ft = vim.api.nvim_get_option_value("filetype", { buf = current_buf })

				-- Use oldContent from diff engine instead of making nvim API calls
				local original_line_width = vim.fn.strdisplaywidth(line_diff.oldContent or "")

				-- Create overlay window positioned over the entire line with minimum width of original line
				if line_diff.content and line_diff.content ~= "" then
					local overlay_win, overlay_buf, bytes_trimmed = create_overlay_window(
						current_win,
						nvim_line,
						0,
						line_diff.content,
						syntax_ft,
						nil,
						original_line_width
					)
					table.insert(completion_windows, { win_id = overlay_win, buf_id = overlay_buf })

					-- Adjust highlight range for any trimmed bytes due to horizontal scroll
					local ov_line = vim.api.nvim_buf_get_lines(overlay_buf, 0, 1, false)[1] or ""
					local ov_len = #ov_line
					local start_col = math.max(0, (line_diff.colStart or 0) - (bytes_trimmed or 0))
					local end_col =
						math.max(start_col, math.min(ov_len, (line_diff.colEnd or start_col) - (bytes_trimmed or 0)))
					if end_col > start_col then
						vim.api.nvim_buf_set_extmark(overlay_buf, daemon.get_namespace_id(), 0, start_col, {
							end_col = end_col,
							hl_group = "cursortabhl_addition",
						})
					end
				end
			elseif line_diff.type == "modification" then
				local line_content = vim.api.nvim_buf_get_lines(current_buf, nvim_line, nvim_line + 1, false)[1] or ""
				local syntax_ft = vim.api.nvim_get_option_value("filetype", { buf = current_buf })

				-- 1. Highlight existing line with red background
				if line_content ~= "" then
					local extmark_id =
						vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), nvim_line, 0, {
							end_col = #line_content,
							hl_group = "cursortabhl_deletion",
							hl_mode = "combine",
						})
					table.insert(completion_extmarks, { buf = current_buf, extmark_id = extmark_id })
				end

				-- 2. Create side-by-side overlay window to the right of the line
				if line_diff.content and line_diff.content ~= "" then
					local line_width = vim.fn.strdisplaywidth(line_content)
					local overlay_win, overlay_buf, _ = create_overlay_window(
						current_win,
						nvim_line,
						line_width + 2,
						line_diff.content,
						syntax_ft,
						"cursortabhl_modification",
						nil
					)
					table.insert(completion_windows, { win_id = overlay_win, buf_id = overlay_buf })
				end
			elseif line_diff.type == "addition" then
				-- Phase 2: addition - Virtual line + overlay window
				local syntax_ft = vim.api.nvim_get_option_value("filetype", { buf = current_buf })
				local buf_line_count = vim.api.nvim_buf_line_count(current_buf)

				-- Calculate the adjusted line position accounting for previous additions
				-- Ensure it never goes negative (clamp to 0)
				local adjusted_nvim_line = math.max(0, nvim_line - addition_offset)

				-- Create a single virtual line at the correct position
				local virtual_extmark_id
				local overlay_line
				if nvim_line >= buf_line_count then
					-- Addition is beyond buffer - place at end
					local last_existing_line = buf_line_count - 1
					virtual_extmark_id =
						vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), last_existing_line, 0, {
							virt_lines = { { { "", "Normal" } } },
							virt_lines_above = false, -- Place below existing content
						})
					overlay_line = buf_line_count -- Position after last existing line
				else
					-- Addition is within buffer - place above the target line
					virtual_extmark_id =
						vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), adjusted_nvim_line, 0, {
							virt_lines = { { { "", "Normal" } } },
							virt_lines_above = true, -- Place above the target line
						})
					-- Overlay should match where the virtual line was placed
					overlay_line = adjusted_nvim_line
				end
				table.insert(completion_extmarks, { buf = current_buf, extmark_id = virtual_extmark_id })

				-- Create overlay window over the virtual line with addition background
				if line_diff.content and line_diff.content ~= "" then
					local overlay_win, overlay_buf, _ = create_overlay_window(
						current_win,
						overlay_line,
						0,
						line_diff.content,
						syntax_ft,
						"cursortabhl_addition",
						nil
					)
					table.insert(completion_windows, { win_id = overlay_win, buf_id = overlay_buf })
				end

				addition_offset = addition_offset + 1
			elseif line_diff.type == "modification_group" then
				-- Handle grouped consecutive modifications as inline replacements
				-- Each line in the group gets an overlay covering the original content
				local syntax_ft = vim.api.nvim_get_option_value("filetype", { buf = current_buf })

				if line_diff.groupLines and #line_diff.groupLines > 0 then
					for i, new_line_content in ipairs(line_diff.groupLines) do
						-- Calculate the absolute line number for this line in the group
						local group_line_num = line_diff.startLine + i - 1
						local abs_line_num = (diff_result.startLine or 1) + group_line_num - 1
						local line_nvim = abs_line_num - 1 -- Convert to 0-based for nvim API

						-- Get original line content and width
						local original_content =
							vim.api.nvim_buf_get_lines(current_buf, line_nvim, line_nvim + 1, false)[1] or ""
						local original_width = vim.fn.strdisplaywidth(original_content)

						-- Create overlay window covering the entire line
						if new_line_content and new_line_content ~= "" then
							local overlay_win, overlay_buf, _ = create_overlay_window(
								current_win,
								line_nvim,
								0,
								new_line_content,
								syntax_ft,
								nil,
								original_width
							)
							table.insert(completion_windows, { win_id = overlay_win, buf_id = overlay_buf })
						elseif original_content ~= "" then
							-- New content is empty but original has content - show deletion indicator
							-- Create an overlay that covers the original with a deletion marker
							local overlay_win, overlay_buf, _ = create_overlay_window(
								current_win,
								line_nvim,
								0,
								string.rep(" ", original_width),
								nil,
								"cursortabhl_deletion",
								original_width
							)
							table.insert(completion_windows, { win_id = overlay_win, buf_id = overlay_buf })
						end
					end
				end
			elseif line_diff.type == "addition_group" then
				-- Handle grouped consecutive additions
				local syntax_ft = vim.api.nvim_get_option_value("filetype", { buf = current_buf })
				local buf_line_count = vim.api.nvim_buf_line_count(current_buf)

				-- Create virtual lines for the entire addition group
				-- The key insight: we need to create exactly #groupLines virtual lines
				-- at the position where the first addition should appear

				local first_addition_line = (diff_result.startLine or 1) + line_diff.startLine - 1
				local first_virtual_line = first_addition_line - 1 -- Convert to 0-based for nvim API

				-- Calculate the adjusted line position accounting for previous additions
				-- Ensure it never goes negative (clamp to 0)
				local adjusted_first_virtual_line = math.max(0, first_virtual_line - addition_offset)

				-- Create all virtual lines as a single extmark at the first addition position
				local virt_lines_array = {}
				for _ = 1, #line_diff.groupLines do
					table.insert(virt_lines_array, { { "", "Normal" } })
				end

				local virtual_extmark_id
				local overlay_line
				if first_virtual_line >= buf_line_count then
					-- All additions are beyond buffer - place at end
					local last_existing_line = buf_line_count - 1
					virtual_extmark_id =
						vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), last_existing_line, 0, {
							virt_lines = virt_lines_array,
							virt_lines_above = false, -- Place below existing content
						})
					overlay_line = buf_line_count
				else
					-- Additions start within buffer - place above the target line
					virtual_extmark_id = vim.api.nvim_buf_set_extmark(
						current_buf,
						daemon.get_namespace_id(),
						adjusted_first_virtual_line,
						0,
						{
							virt_lines = virt_lines_array,
							virt_lines_above = true, -- Place above the target line
						}
					)
					-- Overlay should match where the virtual lines were placed
					overlay_line = adjusted_first_virtual_line
				end

				table.insert(completion_extmarks, { buf = current_buf, extmark_id = virtual_extmark_id })

				-- Create overlay windows for the addition group
				if line_diff.groupLines and #line_diff.groupLines > 0 then
					local overlay_win, overlay_buf, _ = create_overlay_window(
						current_win,
						overlay_line,
						0,
						line_diff.groupLines,
						syntax_ft,
						"cursortabhl_addition",
						nil
					)
					table.insert(completion_windows, { win_id = overlay_win, buf_id = overlay_buf })
				end

				addition_offset = addition_offset + (1 + line_diff.endLine - line_diff.startLine)
			end
		end
	end
end

-- Function to show cursor prediction jump text (called from Go)
---@param line_num integer Predicted line number (1-indexed)
local function show_cursor_prediction(line_num)
	-- Get current buffer and window info
	---@type integer
	local current_buf = vim.api.nvim_get_current_buf()
	---@type integer
	local current_win = vim.api.nvim_get_current_win()
	---@type table
	local win_config = vim.api.nvim_win_get_config(current_win)

	-- Don't show preview in floating windows
	if win_config.relative ~= "" then
		return
	end

	-- Go now uses 1-indexed line numbers, same as Neovim
	---@type integer
	local nvim_line_num = line_num

	-- Check if the predicted line is visible in the current viewport
	---@type integer
	local first_visible_line = vim.fn.line("w0")
	---@type integer
	local last_visible_line = vim.fn.line("w$")
	---@type integer
	local total_lines = vim.api.nvim_buf_line_count(current_buf)
	---@type integer
	local current_line = vim.fn.line(".")

	-- Ensure the line number is valid
	if nvim_line_num < 1 or nvim_line_num > total_lines then
		return
	end

	---@type CursortabConfig
	local cfg = config.get()

	if nvim_line_num >= first_visible_line and nvim_line_num <= last_visible_line then
		-- Line is visible
		---@type string
		local line_content = vim.api.nvim_buf_get_lines(current_buf, line_num - 1, line_num, false)[1] or ""
		---@type integer
		local line_length = #line_content

		jump_text_extmark_id =
			vim.api.nvim_buf_set_extmark(current_buf, daemon.get_namespace_id(), line_num - 1, line_length, {
				virt_text = {
					{ " " .. cfg.ui.jump.symbol, "cursortabhl_jump_symbol" },
					{ cfg.ui.jump.text, "cursortabhl_jump_text" },
				},
				virt_text_pos = "overlay",
				hl_mode = "combine",
			})
		jump_text_buf = current_buf
	else
		-- Line is not visible - show directional arrow with distance
		---@type integer
		local win_width = vim.api.nvim_win_get_width(current_win)
		---@type integer
		local win_height = vim.api.nvim_win_get_height(current_win)

		-- Determine direction and calculate distance
		---@type boolean
		local is_below = nvim_line_num > last_visible_line
		---@type integer
		local distance = math.abs(nvim_line_num - current_line)

		-- Build the display text
		---@type string
		local display_text = cfg.ui.jump.text
		if cfg.ui.jump.show_distance then
			display_text = display_text .. "(" .. distance .. " lines) "
		end

		-- Create a scratch buffer for the arrow indicator
		absolute_jump_buf = vim.api.nvim_create_buf(false, true)
		vim.api.nvim_buf_set_lines(absolute_jump_buf, 0, -1, false, { display_text })
		vim.api.nvim_set_option_value("modifiable", false, { buf = absolute_jump_buf })

		-- Calculate position - center horizontally, top or bottom vertically
		---@type integer
		local text_width = vim.fn.strdisplaywidth(display_text)
		---@type integer
		local col = math.max(0, math.floor((win_width - text_width) / 2))
		---@type integer
		local row = is_below and (win_height - 2) or 1 -- Bottom or top with some padding

		-- Create floating window for absolute positioning
		absolute_jump_win = vim.api.nvim_open_win(absolute_jump_buf, false, {
			relative = "win",
			win = current_win,
			row = row,
			col = col,
			width = text_width,
			height = 1,
			style = "minimal",
			zindex = 1,
			focusable = false,
		})

		-- Set window background to match cursortabhl_jump_text highlight
		vim.api.nvim_set_option_value("winhighlight", "Normal:cursortabhl_jump_text", { win = absolute_jump_win })
	end
end

-- Public API

-- Helper function to close all UI (matches original ensure_close_all)
function ui.ensure_close_all()
	ensure_close_cursor_prediction()
	ensure_close_completion()
	clear_expected_line_state()
end

-- Show completion diff highlighting
---@param diff_result DiffResult Completion diff result from Go daemon
function ui.show_completion(diff_result)
	has_completion = true
	ui.ensure_close_all()
	show_completion(diff_result)
end

-- Show cursor prediction jump text
---@param line_num integer Predicted line number (1-indexed)
function ui.show_cursor_prediction(line_num)
	has_cursor_prediction = true
	ui.ensure_close_all()
	show_cursor_prediction(line_num)
end

-- Close all UI elements and reset state (for on_reject)
function ui.close_all()
	ui.ensure_close_all()
	has_completion = false
	has_cursor_prediction = false
end

-- Check if completion is visible
---@return boolean
function ui.has_completion()
	return has_completion
end

-- Check if cursor prediction is visible
---@return boolean
function ui.has_cursor_prediction()
	return has_cursor_prediction
end

-- Check if typed content matches the expected completion (for partial typing optimization)
-- Returns true if current line is a valid progression toward the expected completion
---@param line_num integer Current cursor line (1-indexed)
---@param current_content string Current line content
---@return boolean
function ui.typing_matches_completion(line_num, current_content)
	if not expected_line or not expected_line_num or not original_len then
		return false
	end
	if line_num ~= expected_line_num then
		return false
	end
	-- Current must be: longer than original AND a prefix of target
	local current_len = #current_content
	if current_len <= original_len then
		return false
	end
	return expected_line:sub(1, current_len) == current_content
end

-- Update the ghost text extmark after user typed matching content
-- This avoids visual glitch where old extmark shifts before daemon re-renders
---@param line_num integer Current cursor line (1-indexed)
---@param current_content string Current line content
function ui.update_ghost_text_for_typing(line_num, current_content)
	if not expected_line or not append_chars_extmark_id or not append_chars_buf then
		return
	end
	if not vim.api.nvim_buf_is_valid(append_chars_buf) then
		return
	end

	-- Calculate remaining ghost text
	local current_len = #current_content
	local remaining_ghost = expected_line:sub(current_len + 1)

	-- Delete old extmark
	pcall(vim.api.nvim_buf_del_extmark, append_chars_buf, daemon.get_namespace_id(), append_chars_extmark_id)

	-- If there's remaining ghost text, create new extmark at end of current line
	if remaining_ghost and remaining_ghost ~= "" then
		local nvim_line = line_num - 1 -- Convert to 0-indexed
		local new_extmark_id =
			vim.api.nvim_buf_set_extmark(append_chars_buf, daemon.get_namespace_id(), nvim_line, current_len, {
				virt_text = { { remaining_ghost, "cursortabhl_completion" } },
				virt_text_pos = "overlay",
				hl_mode = "combine",
			})
		append_chars_extmark_id = new_extmark_id

		-- Also update in completion_extmarks array for proper cleanup
		for i, info in ipairs(completion_extmarks) do
			if info.buf == append_chars_buf and info.extmark_id ~= new_extmark_id then
				-- Replace old entry with new one
				completion_extmarks[i] = { buf = append_chars_buf, extmark_id = new_extmark_id }
				break
			end
		end
	else
		append_chars_extmark_id = nil
	end
end

---Create a scratch buffer window with content (replaces floating window)
---@param title string Window title
---@param content string[] Array of content lines
---@param opts table|nil Optional window configuration overrides
---@return integer win_id, integer buf_id
function ui.create_scratch_window(title, content, opts)
	opts = opts or {}

	-- Store the current window to return to later
	local previous_win = vim.api.nvim_get_current_win()

	-- Create buffer for content
	local buf = vim.api.nvim_create_buf(false, true)
	-- Set buffer name with error handling
	local safe_name = "[" .. (title or "unnamed") .. "]"
	pcall(vim.api.nvim_buf_set_name, buf, safe_name)
	vim.api.nvim_buf_set_lines(buf, 0, -1, false, content)

	-- Set buffer options for scratch buffer behavior
	vim.api.nvim_set_option_value("modifiable", false, { buf = buf })
	vim.api.nvim_set_option_value("readonly", true, { buf = buf })
	vim.api.nvim_set_option_value("filetype", opts.filetype or "markdown", { buf = buf })
	vim.api.nvim_set_option_value("buftype", "nofile", { buf = buf })
	vim.api.nvim_set_option_value("bufhidden", "wipe", { buf = buf })
	vim.api.nvim_set_option_value("swapfile", false, { buf = buf })

	-- Determine window sizing based on options
	local win_height = vim.api.nvim_get_option_value("lines", {})
	local split_height

	if opts.size_mode == "fullscreen" then
		-- Use fullscreen - create a new tab
		vim.cmd("tabnew")
		local win = vim.api.nvim_get_current_win()
		vim.api.nvim_win_set_buf(win, buf)
	elseif opts.size_mode == "fit_content" then
		-- Fit content plus one extra line for padding
		split_height = math.min(#content + 1, math.floor(win_height * 0.8)) -- Cap at 80% of screen
		vim.cmd("split")
		local win = vim.api.nvim_get_current_win()
		vim.api.nvim_win_set_height(win, split_height)
		vim.api.nvim_win_set_buf(win, buf)
	else
		-- Default: use 1/3 of screen height
		split_height = math.floor(win_height * 0.3)
		vim.cmd("split")
		local win = vim.api.nvim_get_current_win()
		vim.api.nvim_win_set_height(win, split_height)
		vim.api.nvim_win_set_buf(win, buf)
	end

	local win = vim.api.nvim_get_current_win()

	-- Move to end of content if specified
	if opts.move_to_end and #content > 0 then
		vim.api.nvim_win_set_cursor(win, { #content, 0 })
	end

	-- Also set up autocmd to clean up when buffer is wiped
	vim.api.nvim_create_autocmd("BufWipeout", {
		buffer = buf,
		callback = function()
			-- Return to the previous window if it's still valid
			if vim.api.nvim_win_is_valid(previous_win) then
				vim.api.nvim_set_current_win(previous_win)
			end
		end,
	})

	return win, buf
end

return ui
