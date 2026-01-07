local deleting_see_also = false

function Header(el)
    -- If we hit "SEE ALSO", start deleting and remove the header itself
    if pandoc.utils.stringify(el):upper() == "SEE ALSO" then
        deleting_see_also = true
        return {} 
    end
    -- If we hit any other header, stop deleting
    deleting_see_also = false
    return el
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
