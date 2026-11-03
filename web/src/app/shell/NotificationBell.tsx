import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Bell, BellOff } from "lucide-react";
import { useState } from "react";
import { Button as AriaButton, Dialog, DialogTrigger, Heading, Popover } from "react-aria-components";
import { $api } from "@/shared/api/client";
import { useServerEvent } from "@/shared/api/events";
import { ago } from "@/shared/format";
import { Button, iconButtonClass, popoverClass } from "@/shared/ui";
import { toast } from "@/shared/ui/toast";
import s from "./shell.module.css";

interface NotificationEvent {
  severity?: string;
  title?: string;
}

/** Колокольчик: непрочитанные уведомления, последние десять, переход к алерту. */
export function NotificationBell() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const list = $api.useQuery("get", "/me/notifications", { params: { query: { limit: 10 } } }, { staleTime: 30_000 });
  const refresh = () => void qc.invalidateQueries({ predicate: (q) => q.queryKey[1] === "/me/notifications" });
  useServerEvent("notification.created", (data) => {
    refresh();
    const e = data as NotificationEvent;
    if (e.severity === "critical" && e.title) {
      toast.error(`Критичный алерт: ${e.title}`);
    }
  });
  useServerEvent("reconnected", refresh);
  const read = $api.useMutation("post", "/me/notifications/read", { onSuccess: refresh });
  const unread = list.data?.unread ?? 0;
  const items = list.data?.items ?? [];
  const label = unread > 0 ? `Уведомления, непрочитанных: ${unread}` : "Уведомления";
  return (
    <DialogTrigger isOpen={open} onOpenChange={setOpen}>
      <AriaButton className={`${iconButtonClass} ${s.bell}`} aria-label={label}>
        <Bell size={18} strokeWidth={1.7} aria-hidden />
        {unread > 0 && (
          <span className={s.bellBadge} aria-hidden>
            {unread > 99 ? "99+" : unread}
          </span>
        )}
      </AriaButton>
      <Popover className={popoverClass} placement="bottom end">
        <Dialog className={s.notificationPanel} aria-label="Уведомления">
          <div className={s.notificationHead}>
            <Heading slot="title" className={s.notificationTitle}>
              Уведомления
            </Heading>
            {unread > 0 && (
              <Button size="sm" variant="ghost" onPress={() => read.mutate({ body: { all: true } })}>
                Прочитать все
              </Button>
            )}
          </div>
          {items.length === 0 ? (
            <div className={s.notifications}>
              <BellOff size={18} strokeWidth={1.7} aria-hidden />
              <div>Новых уведомлений нет</div>
            </div>
          ) : (
            <ul className={s.notificationList}>
              {items.map((n) => (
                <li key={n.id}>
                  <button
                    type="button"
                    className={s.notification}
                    data-unread={!n.readAt}
                    data-severity={n.severity}
                    onClick={() => {
                      if (!n.readAt) {
                        read.mutate({ body: { ids: [n.id] } });
                      }
                      setOpen(false);
                      if (n.alertId) {
                        void navigate({ to: "/alerts", search: { alert: n.alertId } });
                      }
                    }}
                  >
                    <span className={s.notificationText}>{n.title}</span>
                    {n.body && <span className={s.notificationBody}>{n.body}</span>}
                    <span className={s.notificationTime}>
                      {!n.readAt && <span className="visually-hidden">Непрочитано. </span>}
                      {ago(n.createdAt)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Dialog>
      </Popover>
    </DialogTrigger>
  );
}
