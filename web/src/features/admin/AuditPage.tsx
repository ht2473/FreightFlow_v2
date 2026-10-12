import { useInfiniteQuery } from "@tanstack/react-query";
import { ScrollText } from "lucide-react";
import { useState } from "react";
import { Guard } from "@/app/Guard";
import { fetchClient, type Schemas } from "@/shared/api/client";
import { dateTime } from "@/shared/format";
import { actionTitle, auditActions } from "@/shared/names";
import { Badge, Button, EmptyState, Modal, PageHeader, Section, Select, Skeleton, tableClass } from "@/shared/ui";
import s from "./admin.module.css";

type AuditRecord = Schemas["AuditRecord"];

const pageSize = 50;

export function AuditPage() {
  return (
    <Guard permission="audit.view">
      <AuditLog />
    </Guard>
  );
}

const actionOptions = [
  { id: "all", label: "Все действия" },
  ...auditActions.map((a) => ({ id: a, label: actionTitle(a) })),
];

function AuditLog() {
  const [action, setAction] = useState("all");
  const [selected, setSelected] = useState<AuditRecord | null>(null);

  const log = useInfiniteQuery({
    queryKey: ["get", "/admin/audit", { action }],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) => {
      const { data, error } = await fetchClient.GET("/admin/audit", {
        params: { query: { action: action === "all" ? undefined : action, cursor: pageParam, limit: pageSize } },
        signal,
      });
      if (error) {
        throw error;
      }
      return data;
    },
    getNextPageParam: (last) => last.nextCursor,
  });

  const items = log.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <>
      <PageHeader title="Журнал аудита" subtitle="Входы, изменения доступа и действия с данными" />
      <Section flush>
        <div className={s.toolbar}>
          <div className={s.filter}>
            <Select label="Действие" options={actionOptions} value={action} onChange={setAction} />
          </div>
        </div>
        {log.isPending ? (
          <div className={s.pad}>
            <Skeleton height={36} />
          </div>
        ) : items.length === 0 ? (
          <EmptyState icon={<ScrollText size={20} strokeWidth={1.7} />} title="Записей нет">
            {action !== "all" ? "Измените фильтр." : "Здесь появятся входы и изменения доступа."}
          </EmptyState>
        ) : (
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Время</th>
                <th>Кто</th>
                <th>Действие</th>
                <th>Объект</th>
                <th>IP</th>
              </tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.id} className={s.clickable} onClick={() => setSelected(r)}>
                  <td className={s.nowrap}>{dateTime(r.at)}</td>
                  <td>{r.actorLabel}</td>
                  <td>
                    <Badge tone={r.action.endsWith("failed") ? "warn" : "neutral"} plain>
                      {actionTitle(r.action)}
                    </Badge>
                  </td>
                  <td className={s.muted}>{r.entityType ? `${r.entityType} ${r.entityId.slice(0, 8)}` : "—"}</td>
                  <td className={s.mono}>{r.ip ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {log.hasNextPage && (
          <div className={s.more}>
            <Button onPress={() => void log.fetchNextPage()} isDisabled={log.isFetchingNextPage}>
              Показать ещё
            </Button>
          </div>
        )}
      </Section>

      <Modal
        isOpen={selected !== null}
        onOpenChange={(open) => !open && setSelected(null)}
        title={selected ? actionTitle(selected.action) : ""}
        description={selected ? dateTime(selected.at) : undefined}
        actions={<Button onPress={() => setSelected(null)}>Закрыть</Button>}
      >
        {selected && (
          <dl className={s.details}>
            <dt>Кто</dt>
            <dd>{selected.actorLabel}</dd>
            <dt>Объект</dt>
            <dd className={s.mono}>{selected.entityType ? `${selected.entityType}/${selected.entityId}` : "—"}</dd>
            <dt>IP</dt>
            <dd className={s.mono}>{selected.ip ?? "—"}</dd>
            <dt>Браузер</dt>
            <dd>{selected.userAgent || "—"}</dd>
            <dt>Подробности</dt>
            <dd>
              <pre className={s.json}>{JSON.stringify(selected.details, null, 2)}</pre>
            </dd>
          </dl>
        )}
      </Modal>
    </>
  );
}
