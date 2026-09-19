import { useEffect, useState, type FormEvent } from "react";
import { SettingsService, type Settings } from "@/services";
import { useStore } from "@/store";
import { applyTheme, type Theme } from "@/theme";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { AccountBlock } from "@/views/AccountBlock";
import { AboutBlock } from "@/views/AboutBlock";

// What the form holds, apart from the two keys, which go out only.
interface Draft {
    baseURL: string;
    model: string;
    apiKey: string;
    dropApiKey: boolean;
    dawBackend: string;
    dawHost: string;
    dawPort: string;
    dawFeedback: string;
    searchProvider: string;
    searchURL: string;
    searchKey: string;
    dropSearchKey: boolean;
    theme: Theme;
    previewDefault: boolean;
}

function draftOf(settings: Settings): Draft {
    return {
        baseURL: settings.BaseURL,
        model: settings.Model,
        apiKey: "",
        dropApiKey: false,
        dawBackend: settings.DAWBackend,
        dawHost: settings.DAWHost,
        dawPort: String(settings.DAWPort),
        dawFeedback: String(settings.DAWFeedback),
        searchProvider: settings.SearchProvider ?? "",
        searchURL: settings.SearchURL ?? "",
        searchKey: "",
        dropSearchKey: false,
        theme: (settings.Theme || "system") as Theme,
        previewDefault: settings.PreviewByDefault,
    };
}

export function SettingsView({ active }: { active: boolean }) {
    const { settings, reloadSettings } = useStore();
    // The draft follows the saved settings until something is typed; then
    // it is the person's, and a visit to another tab does not throw it away.
    const [held, setHeld] = useState<{ of: Settings | null; draft: Draft; touched: boolean } | null>(null);
    const draft = held && (held.touched || held.of === settings) ? held.draft : settings ? draftOf(settings) : null;
    const [note, setNote] = useState<{ text: string; bad: boolean }>({ text: "", bad: false });
    const [models, setModels] = useState<string[]>([]);

    // Reloaded on each visit only when nothing is half-typed: reading the
    // file over unsaved edits showed up first as the theme snapping back.
    useEffect(() => {
        if (active && !held?.touched) reloadSettings();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [active]);

    if (!draft || !settings) return <section className="h-full" />;

    const edit = (change: Partial<Draft>) => {
        setHeld({ of: settings, draft: { ...draft, ...change }, touched: true });
        setNote({ text: "", bad: false });
    };

    // Applied at once rather than on save: a look you cannot see until
    // you commit to it is one you cannot choose.
    const chooseTheme = (theme: Theme) => {
        applyTheme(theme);
        edit({ theme });
    };

    const save = async (event: FormEvent) => {
        event.preventDefault();
        const outgoing: Settings = {
            BaseURL: draft.baseURL,
            Model: draft.model,
            APIKeySet: false,
            DropAPIKey: draft.dropApiKey,
            DAWBackend: draft.dawBackend,
            DAWHost: draft.dawHost,
            DAWPort: Number(draft.dawPort),
            DAWFeedback: Number(draft.dawFeedback),
            DAWAvailable: [],
            PreviewByDefault: draft.previewDefault,
            Theme: draft.theme,
            SearchProvider: draft.searchProvider,
            SearchURL: draft.searchURL,
            SearchKeySet: false,
            DropSearchKey: draft.dropSearchKey,
            SearchAvailable: [],
        };
        const result = await SettingsService.Save(outgoing, draft.apiKey, draft.searchKey);
        if (result.Error) {
            setNote({ text: result.Error.Message, bad: true });
            return;
        }
        setNote({ text: result.Message, bad: false });
        setHeld(null);
        await reloadSettings();
    };

    const searchOn = draft.searchProvider !== "";

    return (
        <section className="settle h-full overflow-y-auto px-5 pb-6">
            <div className="flex max-w-2xl flex-col gap-4 py-2">
                <AccountBlock active={active} onModels={setModels} onChanged={() => { setHeld(null); reloadSettings(); }} />

                <form onSubmit={save} className="flex flex-col gap-4">
                    <Group title="Language model">
                        <Field label="Endpoint" hint="Any OpenAI-compatible endpoint: a local runtime, or a hosted one.">
                            <Input value={draft.baseURL} spellCheck={false} placeholder="http://localhost:11434/v1" onChange={(e) => edit({ baseURL: e.target.value })} />
                        </Field>
                        <Field label="Model">
                            <Input value={draft.model} spellCheck={false} list="models" onChange={(e) => edit({ model: e.target.value })} />
                            <datalist id="models">{models.map((name) => <option key={name} value={name} />)}</datalist>
                        </Field>
                        <Field label="API key" hint={draft.dropApiKey
                            ? "The saved key will be removed when you save."
                            : settings.APIKeySet
                                ? "A key is saved. Leave this empty to keep it, or type a new one to replace it."
                                : "No key saved. A local model usually needs none."}>
                            {/* Never filled from the backend: a key that never crosses cannot be read off a screen. */}
                            <KeyRow
                                value={draft.apiKey}
                                saved={settings.APIKeySet}
                                dropping={draft.dropApiKey}
                                onChange={(apiKey) => edit({ apiKey, dropApiKey: false })}
                                onDrop={(dropApiKey) => edit({ dropApiKey, apiKey: "" })}
                                label="API key"
                            />
                        </Field>
                    </Group>

                    <Group title="DAW">
                        <Field label="Backend">
                            <Select value={draft.dawBackend} onChange={(e) => edit({ dawBackend: e.target.value })}>
                                {(settings.DAWAvailable ?? []).map((name) => <option key={name} value={name}>{name}</option>)}
                            </Select>
                        </Field>
                        <div className="flex gap-2.5">
                            <Field label="Host">
                                <Input value={draft.dawHost} spellCheck={false} onChange={(e) => edit({ dawHost: e.target.value })} />
                            </Field>
                            <Field label="Sends to">
                                <Input type="number" value={draft.dawPort} onChange={(e) => edit({ dawPort: e.target.value })} />
                            </Field>
                            <Field label="Listens on">
                                <Input type="number" value={draft.dawFeedback} onChange={(e) => edit({ dawFeedback: e.target.value })} />
                            </Field>
                        </div>
                        <Hint>The DAW must be set to send OSC feedback to the listening port, or nothing can be read back. A change here applies after a restart.</Hint>
                    </Group>

                    <Group title="Web search">
                        <Field label="Provider">
                            <Select value={draft.searchProvider} onChange={(e) => edit({ searchProvider: e.target.value })}>
                                <option value="">Off</option>
                                {(settings.SearchAvailable ?? []).map((name) => <option key={name} value={name}>{name}</option>)}
                            </Select>
                        </Field>
                        {searchOn && (
                            <>
                                <Field label="Search API key" hint={draft.dropSearchKey
                                    ? "The saved key will be removed when you save, and search turned off."
                                    : settings.SearchKeySet
                                        ? "A key is saved. Leave this empty to keep it, or type a new one to replace it."
                                        : "No key saved."}>
                                    <KeyRow
                                        value={draft.searchKey}
                                        saved={settings.SearchKeySet}
                                        dropping={draft.dropSearchKey}
                                        onChange={(searchKey) => edit({ searchKey, dropSearchKey: false })}
                                        onDrop={(dropSearchKey) => edit({ dropSearchKey, searchKey: "" })}
                                        label="Search API key"
                                    />
                                </Field>
                                <Field label="Instance URL">
                                    <Input value={draft.searchURL} spellCheck={false} placeholder="Only for a local instance" onChange={(e) => edit({ searchURL: e.target.value })} />
                                </Field>
                            </>
                        )}
                        <Hint>Off means the agent knows only the project. A hosted provider needs a key; a local one needs an address.</Hint>
                    </Group>

                    <Group title="Appearance">
                        <Field label="Theme">
                            {/* A row of options rather than a dropdown: three choices, worth seeing without opening anything. */}
                            <div role="group" aria-label="Theme" className="flex gap-1 self-start rounded-lg bg-secondary p-1">
                                {(["light", "dark", "system"] as Theme[]).map((name) => (
                                    <button
                                        key={name}
                                        type="button"
                                        aria-pressed={draft.theme === name}
                                        onClick={() => chooseTheme(name)}
                                        className={cn(
                                            "rounded-md px-3 py-1 text-sm capitalize transition-colors",
                                            draft.theme === name ? "bg-background text-foreground shadow-lift" : "text-muted-foreground hover:text-foreground",
                                        )}
                                    >
                                        {name}
                                    </button>
                                ))}
                            </div>
                        </Field>
                    </Group>

                    <Group title="Safety">
                        <div className="flex items-center gap-2.5">
                            <Switch id="preview-default" checked={draft.previewDefault} onCheckedChange={(on) => edit({ previewDefault: on })} />
                            <Label htmlFor="preview-default" className="cursor-pointer font-normal">Propose changes before making them, by default</Label>
                        </div>
                        <Hint>Every change can be undone either way. This decides whether you see it first.</Hint>
                    </Group>

                    <div className="flex items-center gap-3">
                        <Button type="submit" className="rounded-full">Save</Button>
                        <span role="status" className={cn("text-[12.5px]", note.bad ? "text-destructive" : "text-faint")}>{note.text}</span>
                    </div>
                </form>

                <AboutBlock active={active} />
            </div>
        </section>
    );
}

export function Group({ title, children }: { title: string; children: React.ReactNode }) {
    return (
        <section className="flex flex-col gap-2.5">
            <h2 className="text-[15px] font-semibold tracking-tight">{title}</h2>
            {children}
        </section>
    );
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
    return (
        <label className="flex min-w-0 flex-1 flex-col gap-1.5">
            <span className="text-[13px] text-muted-foreground">{label}</span>
            {children}
            {hint && <Hint>{hint}</Hint>}
        </label>
    );
}

export function Hint({ children, className }: { children: React.ReactNode; className?: string }) {
    return <p className={cn("text-[12.5px] text-faint", className)}>{children}</p>;
}

// A key field with a way to remove the saved key: an empty field keeps
// it, so removal has to be a deliberate act, and one the person can
// take back before saving.
function KeyRow({ value, saved, dropping, onChange, onDrop, label }: {
    value: string;
    saved: boolean;
    dropping: boolean;
    onChange: (value: string) => void;
    onDrop: (drop: boolean) => void;
    label: string;
}) {
    return (
        <div className="flex gap-2">
            <Input
                type="password"
                value={value}
                spellCheck={false}
                autoComplete="off"
                disabled={dropping}
                placeholder={dropping ? "Removed on save" : undefined}
                onChange={(e) => onChange(e.target.value)}
            />
            {saved && (
                <Button type="button" variant="outline" onClick={() => onDrop(!dropping)} aria-label={dropping ? `Keep the saved ${label}` : `Remove the saved ${label}`}>
                    {dropping ? "Keep" : "Remove"}
                </Button>
            )}
        </div>
    );
}
