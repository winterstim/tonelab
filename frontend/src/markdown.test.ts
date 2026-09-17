import { describe, expect, it } from "vitest";
import { renderMarkdown } from "./markdown";

const html = (text: string) => {
    const div = document.createElement("div");
    div.append(renderMarkdown(text));
    return div.innerHTML;
};

describe("markdown", () => {
    it("lays out emphasis, code, lists and tables", () => {
        const out = html("Track 1 is **0.80** and `pan` is *left*.\n\n- one\n- two\n\n| n | name |\n|---|---|\n| 1 | Guitar |");
        expect(out).toContain("<strong>0.80</strong>");
        expect(out).toContain("<code>pan</code>");
        expect(out).toContain("<em>left</em>");
        expect(out).toContain("<ul><li>one</li><li>two</li></ul>");
        expect(out).toContain("<table class=\"md-table\"><tr><th>n</th><th>name</th></tr><tr><td>1</td><td>Guitar</td></tr></table>");
        expect(out).not.toContain("**");
    });

    it("never turns the model's text into markup", () => {
        const out = html("Set <img src=x onerror=alert(1)> to **<b>bold</b>**");
        expect(out).not.toContain("<img");
        expect(out).not.toContain("<b>");
        expect(out).toContain("&lt;img");
    });
});
