export function format(value: unknown): string {
    if (typeof value === "boolean") return value ? "on" : "off";
    if (typeof value === "number") return value.toFixed(2);
    return String(value);
}

// A few codes change what the user should do next; the rest carry a
// message already written for them.
export function explain(code: string, message: string): string {
    switch (code) {
        case "llm_unreachable":
            return `${message} Check the endpoint in Settings, and that a local model is running.`;
        case "llm_unauthorized":
            return `${message} Check the API key in Settings.`;
        case "daw_command_failed":
            return `${message} Check the DAW is running.`;
        default:
            return message;
    }
}

export function parseJSON(text: string): Record<string, any> { // eslint-disable-line @typescript-eslint/no-explicit-any
    try {
        return JSON.parse(text);
    } catch {
        return {};
    }
}
