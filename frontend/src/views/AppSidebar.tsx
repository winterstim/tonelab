import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { MoreHorizontal, Pencil, Plus, Settings2, Trash2, UserRound } from "lucide-react";
import { HostedService, type ConversationSummary, type HostedStatus } from "@/services";
import { useStore } from "@/store";
import { cn } from "@/lib/utils";
import {
    Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupContent, SidebarGroupLabel,
    SidebarHeader, SidebarMenu, SidebarMenuAction, SidebarMenuButton, SidebarMenuItem, useSidebar,
} from "@/components/ui/sidebar";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import type { Screen } from "@/App";

// Conversations are always in view rather than behind a menu: switching
// is frequent, and a list one glance away costs nothing while typing.
export function AppSidebar({ screen, onScreen: go }: { screen: Screen; onScreen: (screen: Screen) => void }) {
    const { conversations, startConversation, askSignIn } = useStore();
    // As a drawer, choosing anything is also the reason to close it.
    const { setOpenMobile } = useSidebar();
    const onScreen = (next: Screen) => { go(next); setOpenMobile(false); };

    return (
        // Folded, the rail drops its tint and edge: a tinted strip under the
        // traffic lights cut them in half, and a bare column of icons on the
        // window's own background does not.
        <Sidebar collapsible="icon" className="group-data-[side=left]:border-r-0">
            {/* Room for the macOS traffic lights; the whole strip drags. Folded,
                the rail keeps only the lights' own height, since they sit inside
                it and the first icon can come no closer than under them. */}
            <SidebarHeader className="h-[52px] justify-center px-3 [-webkit-app-region:drag] group-data-[collapsible=icon]:h-10 group-data-[collapsible=icon]:px-2">
                <span className="truncate pl-[72px] text-sm font-semibold tracking-tight group-data-[collapsible=icon]:hidden">Tonelab</span>
            </SidebarHeader>
            <SidebarContent>
                <SidebarGroup>
                    <SidebarGroupContent>
                        <SidebarMenu>
                            <SidebarMenuItem>
                                <SidebarMenuButton tooltip="New conversation" aria-label="Start a new conversation" onClick={() => { startConversation(); onScreen("chat"); }}>
                                    <Plus />
                                    <span>New conversation</span>
                                </SidebarMenuButton>
                            </SidebarMenuItem>
                        </SidebarMenu>
                    </SidebarGroupContent>
                </SidebarGroup>
                <SidebarGroup className="group-data-[collapsible=icon]:hidden">
                    <SidebarGroupLabel>Chats</SidebarGroupLabel>
                    <SidebarGroupContent>
                        <SidebarMenu>
                            {conversations.map((summary) => (
                                <Chat key={summary.ID} summary={summary} current={screen === "chat" && summary.Active} onOpen={() => onScreen("chat")} />
                            ))}
                        </SidebarMenu>
                    </SidebarGroupContent>
                </SidebarGroup>
            </SidebarContent>
            <SidebarFooter>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <AccountRow onClick={(signedIn) => { onScreen("settings"); if (!signedIn) askSignIn(); }} />
                    </SidebarMenuItem>
                    <SidebarMenuItem>
                        <SidebarMenuButton tooltip="Settings" isActive={screen === "settings"} onClick={() => onScreen("settings")}>
                            <Settings2 />
                            <span>Settings</span>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarFooter>
        </Sidebar>
    );
}

// Rename and delete sit behind one control on the row; a column of bins
// reads as a column of hazards.
function Chat({ summary, current, onOpen }: { summary: ConversationSummary; current: boolean; onOpen: () => void }) {
    const { openConversation, renameConversation, deleteConversation } = useStore();
    const [editing, setEditing] = useState(false);
    const [draft, setDraft] = useState(summary.Title);
    // The menu hands focus back to its trigger as it closes, which would
    // blur the rename field at once; set while a rename is starting.
    const renaming = useRef(false);
    const field = useRef<HTMLInputElement>(null);

    // Enter commits and the field then unmounts, which fires blur too; the
    // second call must find nothing to do.
    const commit = async () => {
        if (!editing || renaming.current) return;
        setEditing(false);
        const chosen = draft.trim();
        if (chosen === "" || chosen === summary.Title) {
            setDraft(summary.Title);
            return;
        }
        await renameConversation(summary.ID, chosen);
    };

    const onKey = (event: KeyboardEvent<HTMLInputElement>) => {
        if (event.key === "Enter") commit();
        if (event.key === "Escape") { setDraft(summary.Title); setEditing(false); }
    };

    return (
        <SidebarMenuItem>
            {editing ? (
                <input
                    ref={field}
                    autoFocus
                    aria-label="Conversation name"
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                    onBlur={commit}
                    onKeyDown={onKey}
                    className="h-8 w-full rounded-md bg-background px-2 text-sm"
                />
            ) : (
                <SidebarMenuButton isActive={current} onClick={() => { openConversation(summary.ID); onOpen(); }} className="pr-7">
                    <span className="truncate">{summary.Title}</span>
                </SidebarMenuButton>
            )}
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <SidebarMenuAction showOnHover aria-label={`More for ${summary.Title}`}>
                        <MoreHorizontal />
                    </SidebarMenuAction>
                </DropdownMenuTrigger>
                <DropdownMenuContent
                    side="right"
                    align="start"
                    className="min-w-40"
                    onCloseAutoFocus={(event) => {
                        if (!renaming.current) return;
                        event.preventDefault();
                        renaming.current = false;
                        field.current?.focus();
                    }}
                >
                    <DropdownMenuItem onSelect={() => { renaming.current = true; setDraft(summary.Title); setEditing(true); }}>
                        <span className="flex items-center gap-2"><Pencil className="size-3.5" /> Rename</span>
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => deleteConversation(summary.ID)}>
                        <span className="flex items-center gap-2"><Trash2 className="size-3.5" /> Delete</span>
                    </DropdownMenuItem>
                </DropdownMenuContent>
            </DropdownMenu>
        </SidebarMenuItem>
    );
}

// One line about the account, kept in view: who is signed in and on what,
// or an invitation to. Read once and again whenever settings change.
// Signed out, the row starts signing in rather than only showing where
// the button is.
function AccountRow({ onClick }: { onClick: (signedIn: boolean) => void }) {
    const { settings } = useStore();
    const [status, setStatus] = useState<HostedStatus | null>(null);

    useEffect(() => {
        let alive = true;
        HostedService.Status().then((read) => { if (alive) setStatus(read); });
        return () => { alive = false; };
    }, [settings]);

    const signedIn = status?.SignedIn ?? false;
    const line = !status ? "" : signedIn ? (status.Email || "Signed in") : "Sign in with Tonelab";
    const sub = signedIn ? (status?.Plan ? `${status.Plan} plan` : "no plan yet") : "Hosted model, nothing to set up";

    return (
        // A plain icon like the row below it, so the two line up in both the
        // open sidebar and the rail; the folded row keeps its padding for the
        // same reason, and hides its text.
        <SidebarMenuButton size="lg" tooltip={line} onClick={() => onClick(signedIn)} className={cn("group-data-[collapsible=icon]:p-2!", !signedIn && "text-muted-foreground")}>
            <UserRound />
            <span className="flex min-w-0 flex-col leading-tight group-data-[collapsible=icon]:hidden">
                <span className="truncate text-sm">{line}</span>
                <span className="truncate text-xs text-faint">{sub}</span>
            </span>
        </SidebarMenuButton>
    );
}
