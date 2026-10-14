import { Download } from "lucide-react";
import { useState } from "react";
import { Menu, MenuItem, MenuTrigger, Popover } from "react-aria-components";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { Button, menuItemClass, popoverClass } from "@/shared/ui";
import { toast } from "@/shared/ui/toast";
import s from "./facilities.module.css";

type Format = Schemas["ExportRequest"]["format"];

const formats: { id: Format; label: string }[] = [
  { id: "xlsx", label: "Excel (XLSX)" },
  { id: "csv", label: "CSV (UTF-8, запятая)" },
  { id: "geojson", label: "GeoJSON (точки)" },
];

/** Размер файла: «1,2 МБ», «340 КБ». */
export function fileSize(bytes: number): string {
  return bytes >= 1024 * 1024
    ? `${num(bytes / 1024 / 1024, 1)} МБ`
    : `${num(Math.max(1, Math.round(bytes / 1024)))} КБ`;
}

/** Выгрузка текущей выборки: формирует задание и показывает ссылку на готовый файл. */
export function ExportMenu({ filter, sort }: { filter: Schemas["FacilityFilter"]; sort: string | undefined }) {
  const [id, setId] = useState<string | null>(null);
  const create = $api.useMutation("post", "/exports", {
    onSuccess: (e) => setId(e.id),
    onError: (e) => toast.error(problemMessage(e)),
  });
  const job = $api.useQuery(
    "get",
    "/exports/{exportId}",
    { params: { path: { exportId: id ?? "" } } },
    {
      enabled: !!id,
      refetchInterval: (q) => (q.state.data?.status === "done" || q.state.data?.status === "failed" ? false : 1000),
    },
  );
  const e = job.data;
  return (
    <div className={s.exportBox}>
      <MenuTrigger>
        <Button size="sm" isDisabled={create.isPending}>
          <Download size={14} aria-hidden /> Выгрузить
        </Button>
        <Popover className={popoverClass} placement="bottom end">
          <Menu
            aria-label="Формат выгрузки"
            onAction={(key) => create.mutate({ body: { kind: "facilities", format: key as Format, filter, sort } })}
          >
            {formats.map((f) => (
              <MenuItem key={f.id} id={f.id} className={menuItemClass}>
                {f.label}
              </MenuItem>
            ))}
          </Menu>
        </Popover>
      </MenuTrigger>
      {id && (
        <span className={s.exportStatus} role="status">
          {!e || e.status === "queued" || e.status === "running" ? (
            "Выгрузка готовится…"
          ) : e.status === "failed" ? (
            <span className={s.exportError}>Выгрузка не удалась{e.error ? `: ${e.error}` : ""}</span>
          ) : (
            <a href={`/api/v1/exports/${e.id}/file`} download>
              Скачать: {num(e.rows ?? 0)} строк, {fileSize(e.fileSize ?? 0)}
            </a>
          )}
        </span>
      )}
    </div>
  );
}
