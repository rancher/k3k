local deleting_see_also = false

function Header(el)
    -- If we hit "SEE ALSO", start deleting and remove the header itself
    if pandoc.utils.stringify(el):upper() == "SEE ALSO" then
        deleting_see_also = true
        return {} 
    end
    -- If we hit any other header, stop deleting
    deleting_see_also = false

    -- Forces the section markers. Pandoc >= 3.7 writes one extra '=' than
    -- previous versions, as it reserves the single '=' for the document title.
    -- Emitting them ourselves keeps the output stable across pandoc versions.
    local marker = string.rep("=", el.level)
    return pandoc.RawBlock('asciidoc', marker .. " " .. pandoc.utils.stringify(el) .. "\n\n")
end

function BulletList(el)
    if deleting_see_also then
        return {} -- Deletes the list of links
    end
    return el
end

function CodeBlock(el)
    -- Forces the ---- separator
    local content = "----\n" .. el.text .. "\n----\n\n"
    return pandoc.RawBlock('asciidoc', content)
end
