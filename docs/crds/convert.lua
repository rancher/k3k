function Link(el)
    -- Check if the target is an internal anchor (starts with #)
    if el.target:sub(1, 1) == "#" then
        local anchor = el.target:sub(2) -- Remove the '#'
        local label = pandoc.utils.stringify(el.content)
        
        -- Return a RawInline in the <<anchor, label>> format
        return pandoc.RawInline('asciidoc', '<<' .. anchor .. ', ' .. label .. '>>')
    end
    return el
end

-- Handle the curly braces specifically
function Str(el)
    if el.text:find("{") or el.text:find("}") then
        -- Remove backslashes that Pandoc adds to protect braces
        local cleaned = el.text:gsub("\\{", "{"):gsub("\\}", "}")
        return pandoc.RawInline('asciidoc', cleaned)
    end
end

-- Sometimes Pandoc wraps cell content in a way that requires 
-- a "Plain" text override to stop the escaping
function Plain(el)
    for i, v in ipairs(el) do
        if v.t == "Str" then
            v.text = v.text:gsub("\\{", "{"):gsub("\\}", "}")
        end
    end
    return el
end
