import { useEffect, useState } from "react";
import { PanelRight } from "lucide-react";
import { AgentService } from "@/services";
import { cn } from "@/lib/utils";
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Button } from "@/components/ui/button";
import { AppSidebar } from "@/views/AppSidebar";
import { ChatView } from "@/views/ChatView";
import { ActionsPanel } from "@/views/ActionsPanel";
import { SettingsView } from "@/views/SettingsView";
import { ThemeContext, useTheme } from "@/theme";
import { StoreProvider, useStore } from "@/store";

export type Screen = "chat" | "settings";

// How stale the connection light may be. The backend decides what counts
// as connected; this only decides how often it is asked.
const statusInterval = 3000;

export function App() {
    const theme = useTheme();
    return (
        <ThemeContext.Provider value={theme}>
            <StoreProvider>
                <TooltipProvider>
                    <SidebarProvider>
                        <Frame />
                    </SidebarProvider>
                </TooltipProvider>
            </StoreProvider>
        </ThemeContext.Provider>
    );
}

// Conversations live on the left, the thread in the middle, what the
// agent did on the right when asked for. Settings take the middle over;
// the sidebar stays, so the way back is always in view.
function Frame() {
    const [screen, setScreen] = useState<Screen>("chat");
    const [actions, setActions] = useState(false);
    const { conversations } = useStore();
    const title = conversations.find((c) => c.Active)?.Title ?? "New conversation";

    return (
        <>
            <AppSidebar screen={screen} onScreen={setScreen} />
            <SidebarInset className="min-w-0 bg-background">
                {/* The bar is the drag region under a hidden title bar. */}
                <header className="flex h-[52px] flex-none items-center gap-2 px-3 [-webkit-app-region:drag]">
                    <SidebarTrigger className="[-webkit-app-region:no-drag]" />
                    <h1 className="min-w-0 flex-1 truncate text-sm font-medium">
                        {screen === "settings" ? "Settings" : title}
                    </h1>
                    <DAWLight />
                    {screen === "chat" && (
                        <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label="Recent actions"
                            aria-pressed={actions}
                            onClick={() => setActions((open) => !open)}
                            className={cn("[-webkit-app-region:no-drag]", actions && "bg-accent")}
                        >
                            <PanelRight />
                        </Button>
                    )}
                </header>
                <div className="flex min-h-0 flex-1">
                    <div role="tabpanel" aria-label="Chat" className="min-w-0 flex-1" hidden={screen !== "chat"}>
                        <ChatView active={screen === "chat"} onAccount={() => setScreen("settings")} />
                    </div>
                    <div role="tabpanel" aria-label="Settings" className="min-w-0 flex-1" hidden={screen !== "settings"}>
                        <SettingsView active={screen === "settings"} />
                    </div>
                    {screen === "chat" && actions && <ActionsPanel onClose={() => setActions(false)} />}
                </div>
            </SidebarInset>
        </>
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
        <span className="flex items-center gap-2 px-2 text-[13px] text-muted-foreground" title={state.detail} data-connected={state.connected}>
            <span aria-hidden className={cn("size-2 rounded-full", state.connected ? "bg-success" : "bg-faint")} />
            {state.text}
        </span>
    );
}
