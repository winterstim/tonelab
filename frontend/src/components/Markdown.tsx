// The model writes markdown; laid out rather than shown with its
// asterisks, and from escaped text, so nothing it says is markup.
import { useLayoutEffect, useRef } from "react";
import { renderMarkdown } from "@/markdown";

export function Markdown({ text, className }: { text: string; className?: string }) {
    const box = useRef<HTMLDivElement>(null);
    useLayoutEffect(() => {
        box.current?.replaceChildren(renderMarkdown(text));
    }, [text]);
    return <div ref={box} className={className ? `prose-said ${className}` : "prose-said"} />;
}
