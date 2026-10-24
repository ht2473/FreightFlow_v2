import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { dateTime } from "@/shared/format";
import { Button, EmptyState, Modal, PageHeader, Section, Skeleton, TextArea, tableClass } from "@/shared/ui";
import { SegmentedControl } from "@/shared/ui/navigation";
import { toast } from "@/shared/ui/toast";
import s from "./facilities.module.css";

type Report = Schemas["FacilityReport"];
type Status = Report["status"];

export function ReportsPage() {
  return (
    <Guard permission="registry.curate">
      <Reports />
    </Guard>
  );
}

function Reports() {
  const [status, setStatus] = useState<Status>("open");
  const list = $api.useQuery("get", "/facility-reports", { params: { query: { status } } });
  const [closing, setClosing] = useState<{ report: Report; to: "resolved" | "rejected" } | null>(null);
  return (
    <>
      <PageHeader
        title="Сообщения о неточностях"
        subtitle="Сообщения пользователей о записях реестра: проверьте объект, при необходимости уточните его и закройте сообщение"
        actions={
          <Link to="/facilities" className="btn btn-sm">
            К реестру
          </Link>
        }
      />
      <Section
        flush
        actions={
          <SegmentedControl
            label="Состояние"
            options={[
              { id: "open", label: "Открытые" },
              { id: "resolved", label: "Исправленные" },
              { id: "rejected", label: "Не подтвердились" },
            ]}
            value={status}
            onChange={(v) => setStatus(v as Status)}
          />
        }
      >
        {list.isPending ? (
          <div className={s.pad}>
            <Skeleton height={36} />
          </div>
        ) : list.error ? (
          <div className="notice notice-error" role="alert">
            {problemMessage(list.error)}
          </div>
        ) : list.data.length === 0 ? (
          <EmptyState title={status === "open" ? "Открытых сообщений нет" : "Сообщений нет"} />
        ) : (
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Объект</th>
                <th>Сообщение</th>
                <th>Автор, дата</th>
                <th aria-label="Действия" />
              </tr>
            </thead>
            <tbody>
              {list.data.map((r) => (
                <tr key={r.id}>
                  <td>
                    <Link to="/facilities" search={{ open: r.facilityId }}>
                      {r.facilityName}
                    </Link>
                  </td>
                  <td>
                    {r.message}
                    {r.resolution && <div className={s.muted}>{r.resolution}</div>}
                  </td>
                  <td className={s.muted}>
                    {r.author ?? "—"}, {dateTime(r.createdAt)}
                  </td>
                  <td>
                    {r.status === "open" && (
                      <div className={s.actions}>
                        <Button size="sm" onPress={() => setClosing({ report: r, to: "resolved" })}>
                          Исправлено
                        </Button>
                        <Button size="sm" variant="ghost" onPress={() => setClosing({ report: r, to: "rejected" })}>
                          Не подтвердилось
                        </Button>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Section>
      {closing && <CloseDialog closing={closing} onDone={() => setClosing(null)} />}
    </>
  );
}

function CloseDialog({
  closing,
  onDone,
}: {
  closing: { report: Report; to: "resolved" | "rejected" };
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const resolve = $api.useMutation("patch", "/facility-reports/{reportId}", {
    onSuccess: () => {
      toast.success("Сообщение закрыто");
      void qc.invalidateQueries({ queryKey: ["get", "/facility-reports"] });
      onDone();
    },
  });
  const rejected = closing.to === "rejected";
  return (
    <Modal
      isOpen
      onOpenChange={(o) => !o && onDone()}
      title={rejected ? "Сообщение не подтвердилось" : "Неточность исправлена"}
      description={closing.report.message}
      actions={
        <>
          <Button onPress={onDone}>Отмена</Button>
          <Button
            variant="primary"
            isDisabled={(rejected && !text.trim()) || resolve.isPending}
            onPress={() =>
              resolve.mutate({
                params: { path: { reportId: closing.report.id } },
                body: { status: closing.to, resolution: text.trim() || undefined },
              })
            }
          >
            Закрыть сообщение
          </Button>
        </>
      }
    >
      <TextArea
        label={rejected ? "Почему запись верна" : "Что сделано (необязательно)"}
        value={text}
        onChange={setText}
        rows={3}
        errorMessage={resolve.error ? problemMessage(resolve.error) : undefined}
      />
    </Modal>
  );
}
