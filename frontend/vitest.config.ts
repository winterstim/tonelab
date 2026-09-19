import path from "path";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
    plugins: [react()],
    resolve: {
        // The window talks to the backend through one module; tests swap it
        // for the fake so no test ever reaches for the Wails runtime.
        alias: [
            { find: /^@\/services$/, replacement: path.resolve(__dirname, "./src/test/fake.ts") },
            { find: "@", replacement: path.resolve(__dirname, "./src") },
        ],
    },
    test: {
        environment: "jsdom",
        setupFiles: ["./src/test/setup.ts"],
        include: ["src/**/*.test.{ts,tsx}"],
    },
});
