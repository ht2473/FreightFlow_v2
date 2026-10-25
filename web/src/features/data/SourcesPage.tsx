import { useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { Database, MoreHorizontal, Play } from "lucide-react";
import { Menu, MenuItem, MenuTrigger, Popover, Separator } from "react-aria-components";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { ago, num } from "@/shared/format";
import {
  Badge,
  Button,
  cellClass,
  EmptyState,
  IconButton,
  menuItemClass,
  menuSeparatorClass,
  PageHeader,
  popoverClass,
  Section,
  Skeleton,
  tableClass,
} from "@/shared/ui";
import { toast } from "@/shared/ui/toast";
import s from "./data.module.css";
import { freshnessLabel, isActive, runStatus, scheduleLabel } from "./labels";
import { SourceDrawer } from "./SourceDrawer";

type Source = Schemas["SourceInfo"];

export function SourcesPage() {
  return (
    <Guard permission="data.manage">
      <SourceList />
    </Guard>
  );
}

function SourceList() {
  const qc = useQueryClient();
  // Пока идёт загрузка, список обновляется сам.
  const sources = $api.useQuery(
    "get",
    "/data/sources",
    {},
    {
      refetchInterval: (q) => (q.state.data?.some((src) => isActive(src.lastRun?.status)) ? 3000 : false),
    },
  );
  // Открытый источник — в адресе: на него ведут результаты поиска.
  const open = useSearch({ from: "/data" }).source ?? null;
  const navigate = useNavigate({ from: "/data" });
  const setOpen = (source: string | null) => void navigate({ search: { source: source ?? undefined }, replace: true });
  const refresh = () => qc.invalidateQueries({ queryKey: ["get", "/data/sources"] });

  const start = $api.useMutation("post", "/data/sources/{code}/runs", {
    onSuccess: (run) => {
      toast.success("Загрузка поставлена в очередь");
      void refresh();
      void qc.invalidateQueries({
        queryKey: ["get", "/data/sources/{code}/runs", { params: { path: { code: run.source } } }],
      });
    },
    onError: (e) => toast.error(problemMessage(e)),
  });
  const update = $api.useMutation("patch", "/data/sources/{code}", {
    onSuccess: (src) => {
      toast.success(src.enabled ? "Загрузка по расписанию включена" : "Загрузка по расписанию выключена");
      void refresh();
    },
    onError: (e) => toast.error(problemMessage(e)),
  });

  const run = (src: Source, force: boolean) => start.mutate({ params: { path: { code: src.code } }, body: { force } });

  const list = sources.data ?? [];
  const selected = list.find((src) => src.code === open);

  return (
    <>
      <PageHeader title="Источники данных" subtitle="Загрузки, свежесть данных и проверки качества" />
      <Section flush>
        {sources.isPending ? (
          <div className={s.pad}>
            <Skeleton height={36} />
          </div>
        ) : list.length === 0 ? (
          <EmptyState icon={<Database size={20} strokeWidth={1.7} />} title="Источники не подключены">
            Загрузчики появляются в приложении вместе с поддержкой источника.
          </EmptyState>
        ) : (
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Источник</th>
                <th>Расписание</th>
                <th>Данные</th>
                <th>Последний запуск</th>
                <th className={cellClass.right}>Записей</th>
                <th aria-label="Действия" />
              </tr>
            </thead>
            <tbody>
              {list.map((src) => {
                const fr = freshnessLabel[src.freshness];
                const last = src.lastRun;
                const busy = isActive(last?.status);
                return (
                  <tr key={src.code} className={s.clickable} onClick={() => setOpen(src.code)}>
                    <td>
                      <div className={s.strong}>{src.title}</div>
                      <div className={s.muted}>
                        {src.publisher} · <code>{src.code}</code>
                      </div>
                    </td>
                    <td>
                      {scheduleLabel(src.intervalSeconds)}
                      {src.intervalSeconds && !src.enabled && <div className={s.muted}>выключено</div>}
                    </td>
                    <td>
                      <Badge tone={fr.tone}>{fr.label}</Badge>
                      {src.lastSuccessAt && <div className={s.muted}>{ago(src.lastSuccessAt)}</div>}
                    </td>
                    <td>
                      {last ? (
                        <>
                          <Badge tone={runStatus[last.status].tone}>{runStatus[last.status].label}</Badge>
                          <div className={s.muted}>{ago(last.finishedAt ?? last.queuedAt)}</div>
                        </>
                      ) : (
                        <span className={s.muted}>—</span>
                      )}
                    </td>
                    <td className={`${cellClass.right} num`}>{num(src.recordCount)}</td>
                    {/* Действия не открывают карточку источника. */}
                    <td
                      className={cellClass.right}
                      onClick={(e) => e.stopPropagation()}
                      onKeyDown={(e) => e.stopPropagation()}
                    >
                      <div className={s.actions}>
                        <Button size="sm" isDisabled={busy || start.isPending} onPress={() => run(src, false)}>
                          <Play size={13} aria-hidden /> Загрузить
                        </Button>
                        <MenuTrigger>
                          <IconButton label={`Ещё: ${src.title}`}>
                            <MoreHorizontal size={17} />
                          </IconButton>
                          <Popover className={popoverClass} placement="bottom end">
                            <Menu
                              aria-label="Действия с источником"
                              onAction={(k) => {
                                if (k === "force") {
                                  run(src, true);
                                } else if (k === "toggle") {
                                  update.mutate({
                                    params: { path: { code: src.code } },
                                    body: { enabled: !src.enabled },
                                  });
                                } else if (k === "open") {
                                  setOpen(src.code);
                                }
                              }}
                            >
                              <MenuItem id="open" className={menuItemClass}>
                                Запуски и проверки
                              </MenuItem>
                              <MenuItem id="force" className={menuItemClass} isDisabled={busy}>
                                Загрузить заново
                              </MenuItem>
                              {src.intervalSeconds && (
                                <>
                                  <Separator className={menuSeparatorClass} />
                                  <MenuItem id="toggle" className={menuItemClass}>
                                    {src.enabled ? "Выключить расписание" : "Включить расписание"}
                                  </MenuItem>
                                </>
                              )}
                            </Menu>
                          </Popover>
                        </MenuTrigger>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </Section>
      {selected && <SourceDrawer source={selected} onClose={() => setOpen(null)} />}
    </>
  );
}
