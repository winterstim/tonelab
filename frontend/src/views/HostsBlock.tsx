import { useState } from "react";
import { MCPService, type HostSetup } from "@/services";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Group, Hint } from "@/views/SettingsView";

// Tonelab's tools inside Claude, Codex or Cursor, whose own model then
// drives the DAW. One press writes each host's entry; the host's own
// model and plan are used, so nothing here needs a key or a subscription.
export function HostsBlock() {
    const [setups, setSetups] = useState<HostSetup[] | null>(null);
    const [busy, setBusy] = useState(false);

    async function run(action: "Install" | "Remove") {
        setBusy(true);
        try {
            setSetups((await MCPService[action]()) ?? []);
        } finally {
            setBusy(false);
        }
    }

    return (
        <Group title="Use in other apps">
            <p className="text-[13px]">
                Claude Desktop, Claude Code, Codex and Cursor can use Tonelab's tools with their own model.
            </p>
            <div className="flex gap-2">
                <Button type="button" variant="outline" disabled={busy} onClick={() => run("Install")}>Add to these apps</Button>
                <Button type="button" variant="ghost" disabled={busy} onClick={() => run("Remove")}>Remove</Button>
            </div>
            {setups && !setups.some((setup) => setup.Done) && (
                <p role="status" className="text-[12.5px] text-faint">None of these apps is on this computer.</p>
            )}
            {setups && (
                <ul aria-label="Apps" className="flex flex-col gap-1">
                    {setups.map((setup) => (
                        <li key={setup.Host} className={cn("text-[12.5px]", setup.Done ? "text-foreground" : "text-faint")}>
                            <span className="font-medium">{setup.Host}</span>: {setup.Detail}
                        </li>
                    ))}
                </ul>
            )}
            <Hint>Restart an app that was open for it to see the change.</Hint>
        </Group>
    );
}
