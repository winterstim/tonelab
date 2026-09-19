// Translated rather than printed. The tool names and JSON are ours, not
// the user's, and a history that reads like a log is one nobody reads.
import { format, parseJSON } from "./format";

export type StepKind = "list" | "read" | "set" | "undo" | "other";

// The kind of step, so a history can be scanned rather than read.
export function kindOf(tool: string): StepKind {
    switch (tool) {
        case "list_tracks": case "list_fx": case "find_params": case "search": return "list";
        case "get_param": case "get_fx_param": case "fetch_page": return "read";
        case "set_param": case "set_fx_param": return "set";
        case "undo": return "undo";
        default: return "other";
    }
}

export function describeStep(tool: string, args: string, outcome: string, failed: boolean): string {
    const parsed = parseJSON(args);
    const track = parsed.track_id ?? "";
    const name = parsed.param_name ?? "";

    let said: string;
    switch (tool) {
        case "list_tracks": said = "Looked up the tracks"; break;
        case "get_param": said = `Read ${name} on track ${track}`; break;
        case "set_param": said = `Set ${name} on track ${track} to ${format(parsed.value)}`; break;
        case "undo": said = "Asked the DAW to undo"; break;
        case "list_fx": said = `Looked up the effects on track ${track}`; break;
        case "find_params": said = `Searched track ${track} for "${parsed.query ?? ""}"`; break;
        case "search": said = `Searched the web for "${parsed.query ?? ""}"`; break;
        case "fetch_page": said = `Read ${parsed.url ?? "a page"}`; break;
        case "get_fx_param": said = `Read ${fxTarget(outcome, parsed)} on track ${track}`; break;
        case "set_fx_param": said = `Set ${fxTarget(outcome, parsed)} on track ${track} to ${format(parsed.value)}`; break;
        default: said = tool;
    }
    if (failed) {
        const reason = parseJSON(outcome).error?.message;
        return `${said}, refused${reason ? `: ${reason}` : ""}`;
    }
    return said;
}

// The call carries positions and the outcome carries names; a person wants
// the names, so they are taken from the outcome when the call succeeded.
function fxTarget(outcome: string, parsed: Record<string, unknown>): string {
    const value = parseJSON(outcome).value ?? {};
    if (value.fx_name && value.name) return `${value.name} on ${value.fx_name}`;
    return `effect ${parsed.fx_id ?? "?"} parameter ${parsed.param_id ?? "?"}`;
}
