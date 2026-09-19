import path from "path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

// TONELAB_FAKE=1 serves the window against the test fake instead of the
// Wails bindings, so it can be opened in a plain browser to look at.
const fake = process.env.TONELAB_FAKE === "1";

export default defineConfig({
    server: {
        host: "127.0.0.1",
        port: Number(process.env.WAILS_VITE_PORT) || 9245,
        strictPort: true,
    },
    plugins: [react(), tailwindcss(), wails("./bindings")],
    resolve: {
        alias: [
            ...(fake ? [{ find: /^@\/services$/, replacement: path.resolve(__dirname, "./src/test/fake.ts") }] : []),
            { find: "@", replacement: path.resolve(__dirname, "./src") },
        ],
    },
});
