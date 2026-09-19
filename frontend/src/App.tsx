import { useEffect, useState } from "react";
import { MessageSquare, Clock, SlidersHorizontal } from "lucide-react";
import { AgentService } from "@/services";
import { cn } from "@/lib/utils";
import { ChatView } from "@/views/ChatView";
import { HistoryView } from "@/views/HistoryView";
import { SettingsView } from "@/views/SettingsView";
import { ThemeContext, useTheme } from "@/theme";
import { StoreProvider } from "@/store";

type View = "chat" | "history" | "settings";

const tabs: { id: View; label: string; Icon: typeof MessageSquare }[] = [
    { id: "chat", label: "Chat", Icon: MessageSquare },
    { id: "history", label: "History", Icon: Clock },
    { id: "settings", label: "Settings", Icon: SlidersHorizontal },
];

// How stale the connection light may be. The backend decides what counts
// as connected; this only decides how often it is asked.
const statusInterval = 3000;

export function App() {
    const [view, setView] = useState<View>("chat");
    const theme = useTheme();

    return (
        <ThemeContext.Provider value={theme}>
            <StoreProvider>
            <main className="flex h-full flex-col overflow-hidden">
                {/* The bar is the drag region under a hidden title bar. Tabs are
                    centred on the window rather than on what is left beside the
                    macOS traffic lights; the light sits on the right. */}
                <header className="relative flex h-[58px] flex-none items-center justify-end px-5 [-webkit-app-region:drag]">
                    <nav role="tablist" className="absolute left-1/2 flex -translate-x-1/2 gap-1 rounded-full bg-secondary p-1 [-webkit-app-region:no-drag]">
                        {tabs.map(({ id, label, Icon }) => (
                            <button
                                key={id}
                                role="tab"
                                type="button"
                                aria-selected={view === id}
                                onClick={() => setView(id)}
                                className={cn(
                                    "flex items-center gap-1.5 rounded-full px-3 py-1 text-sm transition-colors",
                                    view === id
                                        ? "bg-background text-foreground shadow-lift"
                                        : "text-muted-foreground hover:text-foreground",
                                )}
                            >
                                <Icon className="size-3.5" aria-hidden />
                                {label}
                            </button>
                        ))}
                    </nav>
                    <DAWLight />
                </header>

                {/* Kept mounted rather than routed: a view holds half-typed
                    settings and a scrolled thread, and both should survive a
                    look at another tab. */}
                <div role="tabpanel" aria-label="Chat" className="min-h-0 flex-1" hidden={view !== "chat"}>
                    <ChatView active={view === "chat"} onAccount={() => setView("settings")} />
                </div>
                <div role="tabpanel" aria-label="History" className="min-h-0 flex-1" hidden={view !== "history"}>
                    <HistoryView active={view === "history"} />
                </div>
                <div role="tabpanel" aria-label="Settings" className="min-h-0 flex-1" hidden={view !== "settings"}>
                    <SettingsView active={view === "settings"} />
                </div>
            </main>
            </StoreProvider>
        </ThemeContext.Provider>
    );
}

// Connection is probed rather than assumed, so this reflects an answer from
// the DAW rather than the absence of one.
function DAWLight() {
    const [state, setState] = useState<{ connected: boolean; text: string; detail: string }>({
        connected: false,
        text: "Checking",
        detail: "",
    });

    useEffect(() => {
        let alive = true;
        const ask = async () => {
            try {
                const daw = await AgentService.GetDAWStatus();
                if (alive) setState({ connected: daw.Connected, text: daw.Connected ? "DAW connected" : "DAW not answering", detail: daw.Detail });
            } catch {
                if (alive) setState({ connected: false, text: "Backend not responding", detail: "" });
            }
        };
        ask();
        const timer = window.setInterval(ask, statusInterval);
        return () => {
            alive = false;
            window.clearInterval(timer);
        };
    }, []);

    return (
        <span className="flex items-center gap-2 text-sm text-muted-foreground" title={state.detail} data-connected={state.connected}>
            <span
                aria-hidden
                className={cn("size-2 rounded-full", state.connected ? "bg-success" : "bg-faint")}
            />
            {state.text}
        </span>
    );
}
