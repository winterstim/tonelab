import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";
import { reset } from "./fake";

afterEach(() => {
    cleanup();
    reset();
});

// jsdom has no matchMedia; the window asks it for the system theme.
window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
});

// Radix's switch inside a form watches its own size; jsdom has no observer.
class Still { observe() {} unobserve() {} disconnect() {} }
window.ResizeObserver = Still as unknown as typeof ResizeObserver;
