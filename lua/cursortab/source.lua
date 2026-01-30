local ui = require("cursortab.ui")

local M = {}

---@class CursortabAppendCharsItem
---@field label string
---@field insertText string

---Get append_chars as a base completion item.
---@return CursortabAppendCharsItem|nil
function M.get_append_item()
	local append_chars = ui.get_append_chars()
	if not append_chars or append_chars.text == "" then
		return nil
	end

	return {
		label = append_chars.text,
		insertText = append_chars.text,
	}
end

return M
