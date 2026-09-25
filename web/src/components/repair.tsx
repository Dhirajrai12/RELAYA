import {
  ArrowRightIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
  WrenchIcon,
} from "lucide-react";
import { useId, useMemo, useState, type SubmitEvent } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";

import { ConfirmButton } from "@/components/confirm";
import { kindLabel } from "@/lib/format";
import { SimpleSelect } from "@/components/simple-select";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage } from "@/lib/api";
import { timeAgo } from "@/lib/format";
import {
  useContract,
  useContracts,
  useCreateRepairRule,
  useDeleteRepairRule,
  usePreviewRepair,
  useRepairRules,
  useUpdateRepairRule,
} from "@/lib/queries";
import { useCanManage } from "@/lib/role";
import { cn } from "@/lib/utils";
import type {
  RepairContractResult,
  RepairOp,
  RepairOpKind,
  RepairPreview,
  RepairRule,
  RepairType,
} from "@/lib/types";

// ---- op text ----------------------------------------------------------------------

const kindOptions: { value: RepairOpKind; label: string }[] = [
  { value: "convert", label: "Convert type" },
  { value: "rename", label: "Rename field" },
  { value: "default", label: "Fill in if missing or null" },
  { value: "map", label: "Replace values" },
  { value: "set", label: "Always set" },
  { value: "remove", label: "Remove field" },
];

const typeOptions: { value: RepairType; label: string }[] = [
  { value: "integer", label: "integer" },
  { value: "number", label: "number" },
  { value: "string", label: "text (string)" },
  { value: "boolean", label: "true / false" },
];

const show = (v: unknown) => JSON.stringify(v);

/** One-line description of a change, e.g. "Convert amount to integer". */
export function describeOp(op: RepairOp): string {
  switch (op.op) {
    case "convert":
      return `Convert ${op.path} to ${op.type}`;
    case "rename":
      return `Rename ${op.from} → ${op.to}`;
    case "default":
      return `Fill ${op.path} with ${show(op.value)} when missing or null`;
    case "set":
      return `Set ${op.path} to ${show(op.value)}`;
    case "remove":
      return `Remove ${op.path}`;
    case "map": {
      const pairs = Object.entries(op.values ?? {});
      const first = pairs[0]
        ? `${show(pairs[0][0])} → ${show(pairs[0][1])}`
        : "";
      return `Replace ${op.path} values: ${first}${pairs.length > 1 ? ` (+${pairs.length - 1} more)` : ""}`;
    }
  }
}

// ---- rules card (webhook page) ------------------------------------------------------

/** A webhook's repair rules: list, pause, edit, delete, add. */
export function RepairRulesCard({ webhookId }: { webhookId: string }) {
  const { data, isPending } = useRepairRules(webhookId);
  const canManage = useCanManage();
  const [editing, setEditing] = useState<RepairRule | "new" | null>(null);
  const rules = data?.data ?? [];

  return (
    <Card className="lg:col-span-2" id="repair">
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="min-w-0">
          <CardTitle>Repair rules</CardTitle>
          <CardDescription className="mt-1.5">
            Fix a provider's broken payload before it reaches your endpoints,
            e.g. turn <code className="font-mono text-xs">"100"</code> back into{" "}
            <code className="font-mono text-xs">100</code>. The original event
            is kept as received.
          </CardDescription>
        </div>
        {canManage && (
          <Button size="sm" onClick={() => setEditing("new")}>
            <PlusIcon /> Add
          </Button>
        )}
      </CardHeader>
      <CardContent>
        {isPending ? (
          <Skeleton className="h-16 w-full" />
        ) : rules.length === 0 ? (
          <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
            No repair rules. When an incident opens, use{" "}
            <span className="font-medium text-foreground">Fix with a rule</span>{" "}
            on it, or add one here.
          </div>
        ) : (
          <ul className="divide-y rounded-lg border">
            {rules.map((r) => (
              <RuleRow key={r.id} r={r} onEdit={() => setEditing(r)} />
            ))}
          </ul>
        )}
      </CardContent>
      <RepairRuleDialog
        open={editing !== null}
        onOpenChange={(o) => !o && setEditing(null)}
        webhookId={webhookId}
        rule={editing === "new" ? undefined : (editing ?? undefined)}
      />
    </Card>
  );
}

function RuleRow({ r, onEdit }: { r: RepairRule; onEdit: () => void }) {
  const canManage = useCanManage();
  const update = useUpdateRepairRule();
  const remove = useDeleteRepairRule();
  return (
    <li className="p-4">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1 basis-56">
          <div className="flex flex-wrap items-center gap-2">
            <WrenchIcon className="size-4 shrink-0 text-violet-600 dark:text-violet-400" />
            <span className="min-w-0 break-words font-medium">{r.name}</span>
            <Badge variant="secondary" className="max-w-full truncate">
              {r.event_type || "all event types"}
            </Badge>
            {!r.enabled && <Badge variant="outline">paused</Badge>}
          </div>
          <ul className="mt-2 space-y-0.5">
            {r.ops.map((op, i) => (
              <li
                key={i}
                className="break-all font-mono text-xs text-muted-foreground"
              >
                {describeOp(op)}
              </li>
            ))}
          </ul>
          <div className="mt-2 text-xs text-muted-foreground">
            {r.applied_count === 0
              ? "Not applied yet"
              : `Repaired ${r.applied_count} deliver${r.applied_count === 1 ? "y" : "ies"} · last ${r.last_applied_at ? timeAgo(r.last_applied_at) : ""}`}
          </div>
        </div>
        {canManage && (
          <div className="flex flex-wrap items-center gap-1">
            <Button
              size="sm"
              variant="ghost"
              onClick={() =>
                update.mutateAsync({ id: r.id, enabled: !r.enabled }).then(
                  () =>
                    toast.success(
                      r.enabled
                        ? "Paused: payloads are forwarded as received"
                        : "Resumed",
                    ),
                  (e) => toast.error(errorMessage(e)),
                )
              }
            >
              {r.enabled ? "Pause" : "Resume"}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              aria-label="Edit"
              onClick={onEdit}
            >
              <PencilIcon />
            </Button>
            <ConfirmButton
              variant="ghost"
              destructive
              title={`Delete “${r.name}”?`}
              description="Payloads are forwarded exactly as the provider sends them again. If the provider is still broken, incidents will open again."
              confirmLabel="Delete rule"
              onConfirm={() =>
                remove
                  .mutateAsync(r.id)
                  .then(() => toast.success("Rule deleted"))
              }
            >
              <Trash2Icon />
            </ConfirmButton>
          </div>
        )}
      </div>
    </li>
  );
}

// ---- editor ----------------------------------------------------------------------------

interface Draft {
  op: RepairOpKind;
  path: string;
  type: RepairType;
  from: string;
  to: string;
  valueText: string;
  pairs: { from: string; toText: string }[];
}

function toDraft(op?: RepairOp): Draft {
  return {
    op: op?.op ?? "convert",
    path: op?.path ?? "",
    type: op?.type ?? "integer",
    from: op?.from ?? "",
    to: op?.to ?? "",
    valueText: op?.value === undefined ? "" : show(op.value),
    pairs: Object.entries(op?.values ?? {}).map(([from, v]) => ({
      from,
      toText: show(v),
    })),
  };
}

/** Numbers, true/false, null and JSON parse as such; anything else is text. */
function parseValue(text: string): unknown {
  const t = text.trim();
  if (t === "") return "";
  try {
    return JSON.parse(t);
  } catch {
    return text;
  }
}

function toOp(d: Draft): RepairOp {
  switch (d.op) {
    case "convert":
      return { op: "convert", path: d.path.trim(), type: d.type };
    case "rename":
      return { op: "rename", from: d.from.trim(), to: d.to.trim() };
    case "set":
    case "default":
      return { op: d.op, path: d.path.trim(), value: parseValue(d.valueText) };
    case "remove":
      return { op: "remove", path: d.path.trim() };
    case "map":
      return {
        op: "map",
        path: d.path.trim(),
        values: Object.fromEntries(
          d.pairs
            .filter((p) => p.from !== "")
            .map((p) => [p.from, parseValue(p.toText)]),
        ),
      };
  }
}

export interface RepairRuleSeed {
  name: string;
  event_type: string;
  ops: RepairOp[];
  incident_id?: string;
  sample_event_id?: string | null;
  explanation?: string;
  needs_value?: boolean;
}

/** Create or edit a repair rule, with a dry run on a real event. */
export function RepairRuleDialog({
  open,
  onOpenChange,
  webhookId,
  rule,
  seed,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  webhookId: string;
  rule?: RepairRule;
  seed?: RepairRuleSeed;
  onSaved?: (rule: RepairRule) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        {open && (
          <RuleForm
            key={rule?.id ?? seed?.incident_id ?? "new"}
            webhookId={webhookId}
            rule={rule}
            seed={seed}
            onDone={(r) => {
              onOpenChange(false);
              onSaved?.(r);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function RuleForm({
  webhookId,
  rule,
  seed,
  onDone,
}: {
  webhookId: string;
  rule?: RepairRule;
  seed?: RepairRuleSeed;
  onDone: (r: RepairRule) => void;
}) {
  const create = useCreateRepairRule();
  const update = useUpdateRepairRule();
  const preview = usePreviewRepair();
  const contracts = useContracts(webhookId);
  const listId = useId();

  const initialOps = rule?.ops ?? seed?.ops ?? [{ op: "convert" } as RepairOp];
  const [name, setName] = useState(rule?.name ?? seed?.name ?? "");
  const [eventType, setEventType] = useState(
    rule?.event_type ?? seed?.event_type ?? "",
  );
  const [drafts, setDrafts] = useState<Draft[]>(() => initialOps.map(toDraft));
  const [error, setError] = useState("");
  const [result, setResult] = useState<RepairPreview | null>(null);
  const eventId = seed?.sample_event_id ?? undefined;

  const typeList = contracts.data?.data ?? [];
  const contractId = typeList.find((c) => c.event_type === eventType)?.id ?? "";
  const contract = useContract(contractId);
  const paths = useMemo(
    () => (contract.data?.fields ?? []).map((f) => f.path),
    [contract.data],
  );

  const typeOptionsList = [
    { value: "", label: "All event types" },
    ...Array.from(
      new Set([
        ...typeList.map((c) => c.event_type),
        ...(eventType ? [eventType] : []),
      ]),
    ).map((t) => ({ value: t, label: t })),
  ];

  const patchDraft = (i: number, p: Partial<Draft>) => {
    setDrafts((ds) => ds.map((d, j) => (j === i ? { ...d, ...p } : d)));
    setResult(null);
  };

  async function runPreview() {
    setError("");
    try {
      setResult(
        await preview.mutateAsync({
          webhook_id: webhookId,
          event_type: eventType,
          ops: drafts.map(toOp),
          event_id: eventId,
          rule_id: rule?.id,
        }),
      );
    } catch (err) {
      setResult(null);
      setError(errorMessage(err));
    }
  }

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    setError("");
    const ops = drafts.map(toOp);
    try {
      const saved = rule
        ? await update.mutateAsync({
            id: rule.id,
            name: name.trim(),
            event_type: eventType,
            ops,
          })
        : await create.mutateAsync({
            webhook_id: webhookId,
            name: name.trim(),
            event_type: eventType,
            ops,
            incident_id: seed?.incident_id,
          });
      toast.success(
        rule ? "Rule saved" : "Rule added: new events are repaired from now on",
      );
      onDone(saved);
    } catch (err) {
      setError(errorMessage(err));
    }
  }

  const saving = create.isPending || update.isPending;

  return (
    <form onSubmit={onSubmit} className="grid grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>
          {rule ? `Edit “${rule.name}”` : "Repair rule"}
        </DialogTitle>
        <DialogDescription>
          {seed?.explanation ??
            "Changes applied to the payload before it is forwarded. The stored event stays as the provider sent it."}
        </DialogDescription>
      </DialogHeader>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="grid min-w-0 gap-2">
          <Label htmlFor="rr-name">Name</Label>
          <Input
            id="rr-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Convert amount back to integer"
            required
            maxLength={100}
          />
        </div>
        <div className="grid min-w-0 gap-2">
          <Label htmlFor="rr-type">Applies to</Label>
          <SimpleSelect
            id="rr-type"
            className="w-full"
            value={eventType}
            onChange={(v) => {
              setEventType(v);
              setResult(null);
            }}
            options={typeOptionsList}
          />
        </div>
      </div>

      <datalist id={listId}>
        {paths.map((p) => (
          <option key={p} value={p} />
        ))}
      </datalist>

      <fieldset className="grid min-w-0 gap-3">
        <legend className="mb-2 text-sm font-medium">Changes, in order</legend>
        {drafts.map((d, i) => (
          <OpEditor
            key={i}
            d={d}
            index={i}
            listId={listId}
            onChange={(p) => patchDraft(i, p)}
            onRemove={
              drafts.length > 1
                ? () => setDrafts((ds) => ds.filter((_, j) => j !== i))
                : undefined
            }
            highlightValue={!!seed?.needs_value && i === 0}
            touched={result?.op_changes[i]}
          />
        ))}
        {drafts.length < 20 && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="justify-self-start"
            onClick={() => setDrafts((ds) => [...ds, toDraft()])}
          >
            <PlusIcon /> Add a change
          </Button>
        )}
      </fieldset>

      <div className="grid min-w-0 gap-2 rounded-lg border bg-muted/30 p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="min-w-0 text-sm">
            <span className="font-medium">Try it</span>
            <span className="text-muted-foreground">
              {" "}
              on {eventId ? "the incident's sample event" : "the latest event"}.
              Nothing is saved.
            </span>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={runPreview}
            disabled={preview.isPending}
          >
            {preview.isPending
              ? "Checking…"
              : result
                ? "Check again"
                : "Preview"}
          </Button>
        </div>
        {result && <PreviewResult r={result} />}
      </div>

      {error && <p className="break-words text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="submit" disabled={saving || !name.trim()}>
          {saving ? "Saving…" : rule ? "Save" : "Add rule"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function OpEditor({
  d,
  index,
  listId,
  onChange,
  onRemove,
  highlightValue,
  touched,
}: {
  d: Draft;
  index: number;
  listId: string;
  onChange: (p: Partial<Draft>) => void;
  onRemove?: () => void;
  highlightValue: boolean;
  touched?: number;
}) {
  const id = `op-${index}`;
  const pathInput = (
    label: string,
    value: string,
    set: (v: string) => void,
    suffix = "path",
    placeholder = "payload.amount",
  ) => (
    <div className="grid min-w-0 gap-1.5">
      <Label htmlFor={`${id}-${suffix}`} className="text-xs">
        {label}
      </Label>
      <Input
        id={`${id}-${suffix}`}
        list={listId}
        className="font-mono text-xs"
        value={value}
        onChange={(e) => set(e.target.value)}
        placeholder={placeholder}
        required
      />
    </div>
  );
  return (
    <div className="grid min-w-0 gap-3 rounded-lg border p-3">
      <div className="flex min-w-0 items-center gap-2">
        <span className="text-xs text-muted-foreground">{index + 1}.</span>
        <SimpleSelect
          className="min-w-0 flex-1 sm:max-w-64"
          value={d.op}
          onChange={(v) => onChange({ op: v as RepairOpKind })}
          options={kindOptions}
        />
        {touched !== undefined && (
          <span
            className={cn(
              "ml-auto shrink-0 text-xs",
              touched
                ? "text-emerald-700 dark:text-emerald-400"
                : "text-amber-700 dark:text-amber-400",
            )}
          >
            {touched
              ? `changed ${touched} value${touched === 1 ? "" : "s"}`
              : "no match in this event"}
          </span>
        )}
        {onRemove && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label="Remove change"
            className={cn(touched === undefined && "ml-auto")}
            onClick={onRemove}
          >
            <Trash2Icon />
          </Button>
        )}
      </div>

      {d.op === "convert" && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_12rem]">
          {pathInput("Field", d.path, (v) => onChange({ path: v }))}
          <div className="grid min-w-0 gap-1.5">
            <Label className="text-xs">To</Label>
            <SimpleSelect
              className="w-full"
              value={d.type}
              onChange={(v) => onChange({ type: v as RepairType })}
              options={typeOptions}
            />
          </div>
        </div>
      )}
      {d.op === "rename" && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {pathInput(
            "From",
            d.from,
            (v) => onChange({ from: v }),
            "from",
            "payload.amount_paise",
          )}
          {pathInput(
            "To",
            d.to,
            (v) => onChange({ to: v }),
            "to",
            "payload.amount",
          )}
        </div>
      )}
      {(d.op === "set" || d.op === "default") && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {pathInput("Field", d.path, (v) => onChange({ path: v }))}
          <div className="grid min-w-0 gap-1.5">
            <Label htmlFor={`${id}-value`} className="text-xs">
              Value
            </Label>
            <Input
              id={`${id}-value`}
              className={cn(
                "font-mono text-xs",
                highlightValue && "border-amber-500 ring-2 ring-amber-500/20",
              )}
              value={d.valueText}
              onChange={(e) => onChange({ valueText: e.target.value })}
              placeholder='INR, 0, true or "100"'
            />
          </div>
          <p className="text-xs text-muted-foreground sm:col-span-2">
            {highlightValue && (
              <span className="font-medium text-amber-700 dark:text-amber-400">
                Choose the value your system should get.{" "}
              </span>
            )}
            Numbers and true/false are sent as such; anything else as text.
            Quote a number to send it as text: "100".
          </p>
        </div>
      )}
      {d.op === "remove" &&
        pathInput("Field", d.path, (v) => onChange({ path: v }))}
      {d.op === "map" && (
        <div className="grid min-w-0 gap-2">
          {pathInput(
            "Field",
            d.path,
            (v) => onChange({ path: v }),
            "path",
            "payload.status",
          )}
          {(d.pairs.length ? d.pairs : [{ from: "", toText: "" }]).map(
            (p, j) => {
              const set = (patch: Partial<typeof p>) => {
                const pairs = d.pairs.length
                  ? [...d.pairs]
                  : [{ from: "", toText: "" }];
                pairs[j] = { ...pairs[j], ...patch };
                onChange({ pairs });
              };
              return (
                <div
                  key={j}
                  className="grid min-w-0 grid-cols-[1fr_auto_1fr] items-center gap-2"
                >
                  <Input
                    aria-label="Value received"
                    className="min-w-0 font-mono text-xs"
                    value={p.from}
                    onChange={(e) => set({ from: e.target.value })}
                    placeholder="SUCCESS"
                  />
                  <ArrowRightIcon className="size-4 text-muted-foreground" />
                  <Input
                    aria-label="Replace with"
                    className="min-w-0 font-mono text-xs"
                    value={p.toText}
                    onChange={(e) => set({ toText: e.target.value })}
                    placeholder="captured"
                  />
                </div>
              );
            },
          )}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="justify-self-start"
            onClick={() =>
              onChange({
                pairs: [
                  ...(d.pairs.length ? d.pairs : [{ from: "", toText: "" }]),
                  { from: "", toText: "" },
                ],
              })
            }
          >
            <PlusIcon /> Another value
          </Button>
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        Use dots for nested fields and <code className="font-mono">[]</code> for
        every item of a list: <code className="font-mono">items[].price</code>.
      </p>
    </div>
  );
}

// ---- preview ---------------------------------------------------------------------------

const contractWord: Record<
  RepairContractResult["status"],
  { label: string; cls: string }
> = {
  none: { label: "no active contract", cls: "text-muted-foreground" },
  ok: { label: "matches", cls: "text-emerald-700 dark:text-emerald-400" },
  compatible: {
    label: "matches (new fields)",
    cls: "text-emerald-700 dark:text-emerald-400",
  },
  suspicious: { label: "warning", cls: "text-amber-700 dark:text-amber-400" },
  breaking: { label: "breaking", cls: "text-red-700 dark:text-red-400" },
};

function PreviewResult({ r }: { r: RepairPreview }) {
  const before = r.contract.before;
  const after = r.contract.after;
  const lines = useMemo(
    () => diffLines(pretty(r.before), pretty(r.after)),
    [r.before, r.after],
  );
  return (
    <div className="grid min-w-0 gap-2 text-xs">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="text-muted-foreground">Contract:</span>
        <span className={contractWord[before.status].cls}>
          {contractWord[before.status].label}
        </span>
        <ArrowRightIcon className="size-3 text-muted-foreground" />
        <span className={cn("font-medium", contractWord[after.status].cls)}>
          {contractWord[after.status].label}
        </span>
        <Link
          to={`/events?event=${r.event_id}`}
          className="ml-auto text-muted-foreground underline-offset-4 hover:underline"
        >
          {r.event_type || "event"}
        </Link>
      </div>
      {after.findings.length > 0 && (
        <ul className="grid gap-0.5 text-amber-700 dark:text-amber-400">
          {after.findings.map((f, i) => (
            <li key={i} className="break-all">
              Still {f.severity === "breaking" ? "breaking" : "a warning"}:{" "}
              {kindLabel(f.kind)} at <span className="font-mono">{f.path}</span>
            </li>
          ))}
        </ul>
      )}
      {!r.is_json ? (
        <p className="text-muted-foreground">
          This event isn't JSON, so rules don't apply to it.
        </p>
      ) : !r.changed ? (
        <p className="text-muted-foreground">
          Nothing would change for this event.
        </p>
      ) : (
        <pre className="max-h-72 overflow-auto rounded-md border bg-background p-2 font-mono text-[11px] leading-relaxed">
          {lines.map((l, i) => (
            <div
              key={i}
              className={cn(
                "whitespace-pre",
                l.kind === "+" &&
                  "bg-emerald-500/10 text-emerald-800 dark:text-emerald-300",
                l.kind === "-" &&
                  "bg-red-500/10 text-red-800 dark:text-red-300",
                (l.kind === " " || l.kind === "…") && "text-muted-foreground",
              )}
            >
              {l.kind === "…"
                ? "  …"
                : `${l.kind === " " ? " " : l.kind} ${l.text}`}
            </div>
          ))}
        </pre>
      )}
      {r.other_rules.length > 0 && (
        <p className="text-muted-foreground">
          Also applied first: {r.other_rules.join(", ")}.
        </p>
      )}
      <p className="text-muted-foreground">
        Sensitive fields are masked here, as in the event explorer.
      </p>
    </div>
  );
}

function pretty(v: unknown) {
  return JSON.stringify(v, null, 2) ?? "";
}

/** Line diff (LCS) with unchanged runs trimmed to 2 lines of context. */
function diffLines(
  a: string,
  b: string,
): { kind: "+" | "-" | " " | "…"; text: string }[] {
  const x = a.split("\n");
  const y = b.split("\n");
  if (x.length * y.length > 400_000)
    return y.map((text) => ({ kind: " " as const, text }));
  const dp = Array.from(
    { length: x.length + 1 },
    () => new Uint32Array(y.length + 1),
  );
  for (let i = x.length - 1; i >= 0; i--)
    for (let j = y.length - 1; j >= 0; j--)
      dp[i][j] =
        x[i] === y[j]
          ? dp[i + 1][j + 1] + 1
          : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const out: { kind: "+" | "-" | " "; text: string }[] = [];
  let i = 0;
  let j = 0;
  while (i < x.length || j < y.length) {
    if (i < x.length && j < y.length && x[i] === y[j]) {
      out.push({ kind: " ", text: x[i] });
      i++;
      j++;
    } else if (
      j < y.length &&
      (i === x.length || dp[i][j + 1] >= dp[i + 1][j])
    ) {
      out.push({ kind: "+", text: y[j++] });
    } else {
      out.push({ kind: "-", text: x[i++] });
    }
  }
  // Keep 2 lines of context around changes.
  const keep = out.map(() => false);
  out.forEach((l, k) => {
    if (l.kind !== " ")
      for (let d = -2; d <= 2; d++) if (out[k + d]) keep[k + d] = true;
  });
  const trimmed: { kind: "+" | "-" | " " | "…"; text: string }[] = [];
  out.forEach((l, k) => {
    if (keep[k]) trimmed.push(l);
    else if (trimmed.at(-1)?.kind !== "…")
      trimmed.push({ kind: "…", text: "…" });
  });
  return trimmed;
}
