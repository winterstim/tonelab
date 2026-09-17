// A small markdown layout for what the model writes: paragraphs, emphasis,
// inline code, lists, tables and headings. Written here rather than pulled
// in, because the model's markdown is a narrow dialect and every byte of
// it is untrusted: the text is escaped first and only our own tags are
// ever produced, so nothing the model says can become markup.

export function renderMarkdown(text: string): DocumentFragment {
    const fragment = document.createDocumentFragment();
    const lines = text.replace(/\r/g, "").split("\n");
    let i = 0;
    while (i < lines.length) {
        const line = lines[i];
        if (line.trim() === "") {
            i++;
            continue;
        }
        if (isTableRow(line) && i + 1 < lines.length && isTableRule(lines[i + 1])) {
            const rows: string[] = [];
            while (i < lines.length && isTableRow(lines[i])) {
                rows.push(lines[i]);
                i++;
            }
            fragment.append(table(rows));
            continue;
        }
        if (/^\s*([-*+]|\d+[.)])\s+/.test(line)) {
            const ordered = /^\s*\d+[.)]\s+/.test(line);
            const list = document.createElement(ordered ? "ol" : "ul");
            while (i < lines.length && /^\s*([-*+]|\d+[.)])\s+/.test(lines[i])) {
                const item = document.createElement("li");
                item.append(inline(lines[i].replace(/^\s*([-*+]|\d+[.)])\s+/, "")));
                list.append(item);
                i++;
            }
            fragment.append(list);
            continue;
        }
        const heading = /^(#{1,6})\s+(.*)$/.exec(line);
        if (heading) {
            const strong = document.createElement("p");
            strong.className = "md-heading";
            strong.append(inline(heading[2]));
            fragment.append(strong);
            i++;
            continue;
        }
        // A paragraph runs until a blank line or the start of a block.
        const paragraph: string[] = [];
        while (i < lines.length && lines[i].trim() !== "" && !/^\s*([-*+]|\d+[.)])\s+/.test(lines[i]) && !isTableRow(lines[i]) && !/^#{1,6}\s/.test(lines[i])) {
            paragraph.push(lines[i]);
            i++;
        }
        const p = document.createElement("p");
        p.append(inline(paragraph.join("\n")));
        fragment.append(p);
    }
    return fragment;
}

function isTableRow(line: string): boolean {
    const t = line.trim();
    return t.startsWith("|") && t.endsWith("|") && t.length > 2;
}

function isTableRule(line: string): boolean {
    return /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(line);
}

function cells(row: string): string[] {
    return row.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => c.trim());
}

function table(rows: string[]): HTMLTableElement {
    const t = document.createElement("table");
    t.className = "md-table";
    const head = document.createElement("tr");
    for (const cell of cells(rows[0])) {
        const th = document.createElement("th");
        th.append(inline(cell));
        head.append(th);
    }
    t.append(head);
    for (const row of rows.slice(2)) {
        const tr = document.createElement("tr");
        for (const cell of cells(row)) {
            const td = document.createElement("td");
            td.append(inline(cell));
            tr.append(td);
        }
        t.append(tr);
    }
    return t;
}

// inline handles **bold**, *italic*, `code` and line breaks, producing
// text nodes and our own elements only.
function inline(text: string): DocumentFragment {
    const fragment = document.createDocumentFragment();
    const pattern = /(\*\*[^*\n]+\*\*|`[^`\n]+`|\*[^*\n]+\*|_[^_\n]+_|\n)/g;
    let last = 0;
    for (const match of text.matchAll(pattern)) {
        const at = match.index ?? 0;
        if (at > last) {
            fragment.append(text.slice(last, at));
        }
        const token = match[0];
        if (token === "\n") {
            fragment.append(document.createElement("br"));
        } else if (token.startsWith("**")) {
            const b = document.createElement("strong");
            b.textContent = token.slice(2, -2);
            fragment.append(b);
        } else if (token.startsWith("`")) {
            const code = document.createElement("code");
            code.textContent = token.slice(1, -1);
            fragment.append(code);
        } else {
            const em = document.createElement("em");
            em.textContent = token.slice(1, -1);
            fragment.append(em);
        }
        last = at + token.length;
    }
    if (last < text.length) {
        fragment.append(text.slice(last));
    }
    return fragment;
}
