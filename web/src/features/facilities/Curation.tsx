import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { dateTime } from "@/shared/format";
import { Badge, Button, EmptyState, Modal, Select, Skeleton, TextArea, TextField } from "@/shared/ui";
import { toast } from "@/shared/ui/toast";
import s from "./facilities.module.css";

type Card = Schemas["FacilityCard"];
type Field = Schemas["CurationRequest"]["field"];

export const fieldLabel: Record<Field, string> = {
  name: "Название",
  type_code: "Тип",
  status: "Статус в реестре",
  address: "Адрес",
};

const statusLabel: Record<string, string> = { active: "в реестре", excluded: "исключён" };

const reportStatus: Record<Schemas["FacilityReport"]["status"], { label: string; tone: "warn" | "ok" | "neutral" }> = {
  open: { label: "открыто", tone: "warn" },
  resolved: { label: "исправлено", tone: "ok" },
  rejected: { label: "не подтвердилось", tone: "neutral" },
};

/** Обновить карточку, историю и списки после правки. */
function useRefresh(id: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["get", "/facilities/{facilityId}"] });
    void qc.invalidateQueries({
      queryKey: ["get", "/facilities/{facilityId}/history", { params: { path: { facilityId: id } } }],
    });
    void qc.invalidateQueries({ queryKey: ["get", "/facilities"] });
    void qc.invalidateQueries({ queryKey: ["get", "/facility-reports"] });
  };
}

/** Вкладка «История»: правки редактора и сообщения о неточностях. */
export function History({ c }: { c: Card }) {
  const id = c.facility.id;
  const me = useMe();
  const editor = can(me.data, "registry.curate");
  const refresh = useRefresh(id);
  const history = $api.useQuery("get", "/facilities/{facilityId}/history", { params: { path: { facilityId: id } } });
  const revoke = $api.useMutation("delete", "/facilities/{facilityId}/curations/{curationId}", {
    onSuccess: () => {
      toast.success("Правка отменена");
      refresh();
    },
    onError: (e) => toast.error(problemMessage(e)),
  });
  if (history.isPending) {
    return <Skeleton height={80} />;
  }
  const h = history.data;
  return (
    <div className={s.stack}>
      <p className={s.muted}>
        В реестре с {dateTime(c.facility.firstSeen)}, последнее подтверждение источником — {dateTime(c.lastSeen)}.
      </p>
      {!h || (h.curations.length === 0 && h.reports.length === 0) ? (
        <EmptyState title="Правок и сообщений нет">Запись — по данным источников.</EmptyState>
      ) : (
        <ul className={s.list}>
          {h.curations.map((cur) => (
            <li key={cur.id}>
              <div>
                <div>
                  {fieldLabel[cur.field]}: {cur.field === "status" ? statusLabel[cur.value ?? ""] : cur.value}
                  {cur.revokedAt && <Badge plain>отменена</Badge>}
                </div>
                <div className={s.muted}>
                  {cur.reason} · {cur.author ?? "редактор"}, {dateTime(cur.createdAt)}
                  {cur.revokedAt && ` · отменил ${cur.revokedBy ?? "редактор"} ${dateTime(cur.revokedAt)}`}
                </div>
              </div>
              {editor && !cur.revokedAt && (
                <Button
                  size="sm"
                  variant="ghost"
                  onPress={() => revoke.mutate({ params: { path: { facilityId: id, curationId: cur.id } } })}
                >
                  Отменить
                </Button>
              )}
            </li>
          ))}
          {h.reports.map((r) => (
            <li key={r.id}>
              <div>
                <div>
                  Сообщение: {r.message}{" "}
                  <Badge tone={reportStatus[r.status].tone}>{reportStatus[r.status].label}</Badge>
                </div>
                <div className={s.muted}>
                  {r.author ?? "пользователь"}, {dateTime(r.createdAt)}
                  {r.resolution && ` · ${r.resolution}`}
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** «Сообщить о неточности» — для всех, кто видит реестр. */
export function ReportIssue({ c }: { c: Card }) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const refresh = useRefresh(c.facility.id);
  const send = $api.useMutation("post", "/facilities/{facilityId}/reports", {
    onSuccess: () => {
      toast.success("Сообщение передано редактору");
      setOpen(false);
      setText("");
      refresh();
    },
  });
  return (
    <>
      <Button size="sm" variant="ghost" onPress={() => setOpen(true)}>
        Сообщить о неточности
      </Button>
      <Modal
        isOpen={open}
        onOpenChange={setOpen}
        title="Сообщить о неточности"
        description="Редактор реестра проверит запись и исправит её или ответит, почему она верна."
        actions={
          <>
            <Button onPress={() => setOpen(false)}>Отмена</Button>
            <Button
              variant="primary"
              isDisabled={!text.trim() || send.isPending}
              onPress={() =>
                send.mutate({ params: { path: { facilityId: c.facility.id } }, body: { message: text.trim() } })
              }
            >
              Отправить
            </Button>
          </>
        }
      >
        <TextArea
          label="Что неверно"
          value={text}
          onChange={setText}
          placeholder="Например: склад закрыт, по адресу — торговый центр"
          errorMessage={send.error ? problemMessage(send.error) : undefined}
        />
      </Modal>
    </>
  );
}

/** «Уточнить» — правка поля редактором с причиной. */
export function CurateFacility({ c }: { c: Card }) {
  const [open, setOpen] = useState(false);
  const [field, setField] = useState<Field>("name");
  const [value, setValue] = useState("");
  const [reason, setReason] = useState("");
  const refresh = useRefresh(c.facility.id);
  const facets = $api.useQuery("get", "/facilities/facets", {}, { enabled: open, staleTime: 60_000 });
  const save = $api.useMutation("post", "/facilities/{facilityId}/curations", {
    onSuccess: () => {
      toast.success("Правка сохранена");
      setOpen(false);
      setReason("");
      refresh();
    },
  });
  const start = (f: Field) => {
    setField(f);
    const item = c.facility;
    setValue(
      f === "name"
        ? (item.name ?? "")
        : f === "type_code"
          ? item.type
          : f === "address"
            ? (item.address ?? "")
            : c.status === "excluded"
              ? "active"
              : "excluded",
    );
  };
  return (
    <>
      <Button
        size="sm"
        onPress={() => {
          start("name");
          setOpen(true);
        }}
      >
        Уточнить
      </Button>
      <Modal
        isOpen={open}
        onOpenChange={setOpen}
        title="Уточнить запись"
        description="Правка действует поверх данных источников и сохраняется при их обновлении; её можно отменить во вкладке «История»."
        actions={
          <>
            <Button onPress={() => setOpen(false)}>Отмена</Button>
            <Button
              variant="primary"
              isDisabled={!value.trim() || !reason.trim() || save.isPending}
              onPress={() =>
                save.mutate({
                  params: { path: { facilityId: c.facility.id } },
                  body: { field, value: value.trim(), reason: reason.trim() },
                })
              }
            >
              Сохранить
            </Button>
          </>
        }
      >
        <div className={s.stack}>
          <Select
            label="Поле"
            options={(Object.keys(fieldLabel) as Field[]).map((f) => ({ id: f, label: fieldLabel[f] }))}
            value={field}
            onChange={(f) => start(f as Field)}
          />
          {field === "type_code" ? (
            <Select
              label="Тип"
              options={(facets.data?.types ?? []).map((t) => ({ id: t.value, label: t.label }))}
              value={value}
              onChange={setValue}
            />
          ) : field === "status" ? (
            <Select
              label="Статус"
              options={[
                { id: "excluded", label: "Исключить из реестра" },
                { id: "active", label: "Вернуть в реестр" },
              ]}
              value={value}
              onChange={setValue}
            />
          ) : (
            <TextField label={fieldLabel[field]} value={value} onChange={setValue} />
          )}
          <TextArea
            label="Причина"
            value={reason}
            onChange={setReason}
            rows={2}
            placeholder="Источник сведений: договор, выезд, сайт компании"
            errorMessage={save.error ? problemMessage(save.error) : undefined}
          />
        </div>
      </Modal>
    </>
  );
}
