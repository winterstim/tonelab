// Shown in the thread when the hosted service refused for want of quota:
// which window ran out, how full it is, and when it refills, so the
// person knows whether to wait an hour or look at their plan. On their
// own endpoint the ordinary error line shows instead; this card is only
// for a refusal that came with the service's usage figures.
import { Progress } from "@/components/ui/progress";
import { whenResets } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { AgentError, UsageWindow } from "@/services";

export function isQuotaRefusal(error: AgentError): boolean {
    return error.Code === "no_active_plan" || (error.Usage !== null && error.Usage !== undefined
        && (error.Code === "quota_exceeded" || error.Code === "daily_limit_reached"));
}

export function QuotaCard({ error, onAccount }: { error: AgentError; onAccount: () => void }) {
    const usage = error.Usage;
    const day = error.Code === "daily_limit_reached";
    const window: UsageWindow | null = usage ? (day ? usage.day : usage.month) : null;
    const percent = window && window.limit > 0 ? Math.min(100, Math.round((window.used / window.limit) * 100)) : 0;

    return (
        <div className="flex max-w-[44ch] flex-col gap-2.5 rounded-2xl bg-card p-4 shadow-lift" role="status">
            <p className="font-medium">
                {error.Code === "no_active_plan"
                    ? "No plan is active"
                    : day ? "Today's share is used up" : "This month's tokens are used up"}
            </p>
            {window && (
                <div className="flex flex-col gap-1.5 text-[13px]">
                    <Progress value={percent} aria-label={day ? "Today" : "This month"} className="bg-secondary" indicatorClassName={cn(percent >= 100 ? "bg-destructive" : "bg-warning")} />
                    <span className="tabular-nums text-muted-foreground">{day ? "Today" : "This month"}: {percent}%, {whenResets(window.resets_at)}</span>
                </div>
            )}
            <p className="text-[13px] text-muted-foreground">{error.Message}</p>
            <button type="button" onClick={onAccount} className="self-start text-[13px] underline underline-offset-4 hover:text-foreground">
                {error.Code === "no_active_plan" ? "Choose a plan" : "Open the account"}
            </button>
        </div>
    );
}
