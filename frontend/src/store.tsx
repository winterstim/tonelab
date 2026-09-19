// What more than one view needs: the settings, the list of conversations
// and the thread on screen. Held here rather than in a view so that
// opening a conversation from History changes what Chat shows, and saving
// settings changes what the composer defaults to.
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { AgentService, SettingsService, type ChatMessage, type ConversationSummary, type Settings } from "@/services";
import { applyTheme, type Theme } from "@/theme";

export interface Thread {
    id: string;
    messages: ChatMessage[];
}

interface Store {
    settings: Settings | null;
    reloadSettings: () => Promise<Settings>;
    conversations: ConversationSummary[];
    refreshConversations: () => Promise<void>;
    thread: Thread;
    // A thread the window has drawn from the backend; the number lets a
    // view tell a new thread from a redraw of the same one.
    generation: number;
    openConversation: (id: string) => Promise<void>;
    startConversation: () => Promise<void>;
    deleteConversation: (id: string) => Promise<void>;
    renameConversation: (id: string, name: string) => Promise<void>;
    // A request to begin signing in from somewhere other than the account
    // block; the block starts the flow when the number changes.
    signInAsked: number;
    askSignIn: () => void;
}

const StoreContext = createContext<Store | null>(null);

export function StoreProvider({ children }: { children: ReactNode }) {
    const [settings, setSettings] = useState<Settings | null>(null);
    const [conversations, setConversations] = useState<ConversationSummary[]>([]);
    const [thread, setThread] = useState<Thread>({ id: "", messages: [] });
    const [generation, setGeneration] = useState(0);
    const [signInAsked, setSignInAsked] = useState(0);
    const askSignIn = useCallback(() => setSignInAsked((n) => n + 1), []);

    const reloadSettings = useCallback(async () => {
        const loaded = await SettingsService.Get();
        setSettings(loaded);
        applyTheme((loaded.Theme || "system") as Theme);
        return loaded;
    }, []);

    const refreshConversations = useCallback(async () => {
        // A Go slice with no elements crosses as null rather than [].
        setConversations((await AgentService.Conversations()) ?? []);
    }, []);

    const draw = useCallback((id: string, messages: ChatMessage[] | null) => {
        setThread({ id, messages: messages ?? [] });
        setGeneration((n) => n + 1);
    }, []);

    const openConversation = useCallback(async (id: string) => {
        const opened = await AgentService.OpenConversation(id);
        await refreshConversations();
        draw(opened.ID, opened.Messages);
    }, [draw, refreshConversations]);

    const startConversation = useCallback(async () => {
        // Starts a thread rather than destroying one: the old conversation
        // stays in the list, which is what the words on the button mean.
        const started = await AgentService.StartConversation();
        await refreshConversations();
        draw(started.ID, []);
    }, [draw, refreshConversations]);

    const deleteConversation = useCallback(async (id: string) => {
        const remaining = await AgentService.DeleteConversation(id);
        await refreshConversations();
        draw(remaining.ID, remaining.Messages);
    }, [draw, refreshConversations]);

    const renameConversation = useCallback(async (id: string, name: string) => {
        await AgentService.RenameConversation(id, name);
        await refreshConversations();
    }, [refreshConversations]);

    // The window draws what the backend already holds, so reopening it
    // after a reload shows the conversation rather than an empty room.
    useEffect(() => {
        // The rule sees a setter called in an effect; the state arrives
        // from the backend asynchronously, which is what effects are for.
        // eslint-disable-next-line react-hooks/set-state-in-effect
        reloadSettings();
        AgentService.CurrentConversation().then((current) => {
            draw(current.ID, current.Messages);
            refreshConversations();
        });
    }, [draw, refreshConversations, reloadSettings]);

    const value = useMemo<Store>(() => ({
        settings, reloadSettings,
        conversations, refreshConversations,
        thread, generation,
        openConversation, startConversation, deleteConversation, renameConversation,
        signInAsked, askSignIn,
    }), [settings, reloadSettings, conversations, refreshConversations, thread, generation, openConversation, startConversation, deleteConversation, renameConversation, signInAsked, askSignIn]);

    return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>;
}

export function useStore(): Store {
    const store = useContext(StoreContext);
    if (!store) throw new Error("useStore outside StoreProvider");
    return store;
}
