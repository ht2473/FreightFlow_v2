import { useQueryClient } from "@tanstack/react-query";
import { Copy, Send, Trash2, Webhook as WebhookIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { can, type Me } from "@/shared/auth";
import { ago, dateTime } from "@/shared/format";
import {
  Badge,
  Button,
  cellClass,
  EmptyState,
  IconButton,
  Modal,
  Section,
  Select,
  Skeleton,
  Switch,
  TextField,
  tableClass,
} from "@/shared/ui";
import { toast } from "@/shared/ui/toast";
import s from "./profile.module.css";

type Settings = Schemas["NotificationSettings"];
type Severity = Schemas["AlertSeverity"];

const hours = Array.from({ length: 24 }, (_, h) => ({ id: String(h), label: `${String(h).padStart(2, "0")}:00` }));

const severityOptions: { id: Severity; label: string }[] = [
  { id: "info", label: "Все, включая информационные" },
  { id: "warning", label: "Предупреждения и критичные" },
  { id: "critical", label: "Только критичные" },
];

const emailModes: { id: Settings["emailMode"]; label: string }[] = [
  { id: "instant", label: "Сразу" },
  { id: "digest", label: "Раз в сутки дайджестом" },
  { id: "off", label: "Не присылать" },
];

export function NotificationsSection({ me }: { me: Me }) {
  return (
    <>
      <SettingsSection manage={can(me, "alerts.manage")} />
      <TelegramSection />
      {can(me, "alerts.own") && <WebhooksSection />}
    </>
  );
}

function SettingsSection({ manage }: { manage: boolean }) {
  const qc = useQueryClient();
  const current = $api.useQuery("get", "/me/notification-settings");
  const save = $api.useMutation("put", "/me/notification-settings", {
    onSuccess: (data) => {
      qc.setQueryData(["get", "/me/notification-settings", {}], data);
      toast.success("Настройки уведомлений сохранены");
    },
  });
  const [form, setForm] = useState<Settings | null>(null);
  useEffect(() => {
    if (current.data && !form) {
      setForm(current.data);
    }
  }, [current.data, form]);
  if (!form) {
    return (
      <Section title="Уведомления">
        <Skeleton height={120} />
      </Section>
    );
  }
  const quiet = form.quietFrom !== undefined && form.quietTo !== undefined;
  const set = (patch: Partial<Settings>) => setForm((f) => (f ? { ...f, ...patch } : f));
  return (
    <Section
      title="Уведомления"
      description="Алерты всегда видны в системе (колокольчик). Здесь — почта и Telegram по каналам ваших правил."
    >
      <div className={s.stack}>
        <Select
          label="Письма об алертах"
          options={emailModes}
          value={form.emailMode}
          onChange={(v) => set({ emailMode: v as Settings["emailMode"] })}
        />
        {form.emailMode === "digest" && (
          <Select
            label="Час дайджеста (по Москве)"
            options={hours}
            value={String(form.digestHour)}
            onChange={(v) => set({ digestHour: Number(v) })}
          />
        )}
        <Select
          label="Почта и Telegram — для алертов"
          options={severityOptions}
          value={form.minSeverity}
          onChange={(v) => set({ minSeverity: v as Severity })}
        />
        <Switch
          isSelected={quiet}
          onChange={(on) => set(on ? { quietFrom: 22, quietTo: 8 } : { quietFrom: undefined, quietTo: undefined })}
        >
          Тихие часы: некритичные письма и сообщения — после их окончания
        </Switch>
        {quiet && (
          <div className={s.row}>
            <Select
              label="С"
              options={hours}
              value={String(form.quietFrom)}
              onChange={(v) => set({ quietFrom: Number(v) })}
            />
            <Select
              label="До"
              options={hours}
              value={String(form.quietTo)}
              onChange={(v) => set({ quietTo: Number(v) })}
            />
          </div>
        )}
        {manage && (
          <>
            <Switch isSelected={form.subscribeAll} onChange={(v) => set({ subscribeAll: v })}>
              Получать уведомления обо всех алертах системы
            </Switch>
            {form.subscribeAll && (
              <Select
                label="Все алерты — начиная с важности"
                options={severityOptions}
                value={form.subscribeSeverity}
                onChange={(v) => set({ subscribeSeverity: v as Severity })}
              />
            )}
          </>
        )}
        {save.error && (
          <div className="notice notice-error" role="alert">
            {problemMessage(save.error)}
          </div>
        )}
        <div className={s.actions}>
          <Button variant="primary" onPress={() => save.mutate({ body: form })} isDisabled={save.isPending}>
            Сохранить
          </Button>
        </div>
      </div>
    </Section>
  );
}

function TelegramSection() {
  const qc = useQueryClient();
  const status = $api.useQuery("get", "/me/telegram");
  const link = $api.useMutation("post", "/me/telegram/link");
  const unlink = $api.useMutation("delete", "/me/telegram", {
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["get", "/me/telegram"] });
      toast.success("Telegram отвязан");
    },
  });
  // Пока открыт код привязки, состояние перечитывается: бот привязывает чат сам.
  const waiting = !!link.data && !status.data?.bound;
  $api.useQuery("get", "/me/telegram", {}, { enabled: waiting, refetchInterval: 3000 });
  const st = status.data;
  return (
    <Section title="Telegram" description="Оповещения от бота FreightFlow в личный чат.">
      {!st ? (
        <Skeleton height={40} />
      ) : !st.configured ? (
        <p className="muted">Бот не настроен на сервере: администратору нужно задать токен бота.</p>
      ) : st.bound ? (
        <div className={s.twoFactor}>
          <Badge tone="ok">Привязан</Badge>
          <span className={s.grow}>
            {st.username ? `@${st.username}` : "Чат привязан"}
            {st.boundAt ? `, ${dateTime(st.boundAt)}` : ""}
          </span>
          <Button size="sm" onPress={() => unlink.mutate({})} isDisabled={unlink.isPending}>
            Отвязать
          </Button>
        </div>
      ) : link.data ? (
        <div className={s.stack}>
          <p>
            Откройте{" "}
            <a href={link.data.url} target="_blank" rel="noreferrer">
              бота в Telegram
            </a>{" "}
            и нажмите «Старт» — или отправьте боту команду <code>/start {link.data.code}</code>. Код действует до{" "}
            {dateTime(link.data.expiresAt)}.
          </p>
          <p className="muted">Страница обновится, когда бот привяжет чат.</p>
        </div>
      ) : (
        <Button onPress={() => link.mutate({})} isDisabled={link.isPending}>
          <Send size={15} aria-hidden /> Привязать Telegram
        </Button>
      )}
      {(link.error || unlink.error) && (
        <div className="notice notice-error" role="alert">
          {problemMessage(link.error ?? unlink.error)}
        </div>
      )}
    </Section>
  );
}

const deliveryTitle: Record<Schemas["WebhookDelivery"]["status"], string> = {
  pending: "ожидает",
  digest: "в дайджесте",
  sent: "доставлено",
  failed: "ошибка",
  skipped: "пропущено",
};

const deliveryTone: Record<Schemas["WebhookDelivery"]["status"], "ok" | "crit" | "neutral"> = {
  pending: "neutral",
  digest: "neutral",
  sent: "ok",
  failed: "crit",
  skipped: "neutral",
};

function WebhooksSection() {
  const qc = useQueryClient();
  const list = $api.useQuery("get", "/me/webhooks");
  const refresh = () => void qc.invalidateQueries({ queryKey: ["get", "/me/webhooks"] });
  const create = $api.useMutation("post", "/me/webhooks");
  const update = $api.useMutation("patch", "/me/webhooks/{webhookId}", { onSuccess: refresh });
  const remove = $api.useMutation("delete", "/me/webhooks/{webhookId}", { onSuccess: refresh });
  const test = $api.useMutation("post", "/me/webhooks/{webhookId}/test", {
    onSuccess: (d) => {
      refresh();
      if (d.status === "sent") {
        toast.success(`Проверка доставлена (${d.responseCode})`);
      } else {
        toast.error(`Проверка не доставлена: ${d.lastError ?? d.responseCode ?? "ошибка"}`);
      }
    },
  });
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [secret, setSecret] = useState<string | null>(null);
  const [log, setLog] = useState<Schemas["Webhook"] | null>(null);
  const error = create.error ?? update.error ?? remove.error ?? test.error;
  return (
    <Section
      title="Вебхуки"
      description="POST с JSON на ваш адрес HTTPS при новых алертах правил с каналом «Вебхук». Запрос подписан HMAC-SHA256."
      flush
      actions={
        <Button size="sm" onPress={() => setAdding(true)} isDisabled={(list.data?.length ?? 0) >= 5}>
          Добавить вебхук
        </Button>
      }
    >
      {list.isPending ? (
        <Skeleton height={60} />
      ) : (list.data ?? []).length === 0 ? (
        <EmptyState icon={<WebhookIcon size={20} strokeWidth={1.7} />} title="Вебхуков нет">
          <p>Вебхук передаёт алерты во внешнюю систему: диспетчерскую, чат, трекер задач.</p>
        </EmptyState>
      ) : (
        <table className={tableClass} aria-label="Вебхуки">
          <thead>
            <tr>
              <th>Вебхук</th>
              <th>Последняя доставка</th>
              <th>Включён</th>
              <th className={cellClass.right}>
                <span className="visually-hidden">Действия</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {(list.data ?? []).map((w) => (
              <tr key={w.id}>
                <td>
                  <div>{w.name}</div>
                  <div className="muted">{w.url}</div>
                </td>
                <td>
                  {w.lastStatus ? (
                    <>
                      <Badge tone={deliveryTone[w.lastStatus]}>{deliveryTitle[w.lastStatus]}</Badge> {ago(w.lastAt)}
                    </>
                  ) : (
                    "ещё не было"
                  )}
                </td>
                <td>
                  <Switch
                    aria-label={`Вебхук «${w.name}» включён`}
                    isSelected={w.enabled}
                    onChange={(enabled) => update.mutate({ params: { path: { webhookId: w.id } }, body: { enabled } })}
                  >
                    <span className="visually-hidden">Включён</span>
                  </Switch>
                </td>
                <td className={cellClass.right}>
                  <span className={s.row}>
                    <Button
                      size="sm"
                      variant="ghost"
                      onPress={() => test.mutate({ params: { path: { webhookId: w.id } } })}
                    >
                      Проверить
                    </Button>
                    <Button size="sm" variant="ghost" onPress={() => setLog(w)}>
                      Журнал
                    </Button>
                    <IconButton
                      label={`Удалить вебхук «${w.name}»`}
                      onPress={() => remove.mutate({ params: { path: { webhookId: w.id } } })}
                    >
                      <Trash2 size={15} />
                    </IconButton>
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {error && (
        <div className="notice notice-error" role="alert">
          {problemMessage(error)}
        </div>
      )}

      <Modal
        isOpen={adding}
        onOpenChange={setAdding}
        title="Новый вебхук"
        description="Адрес HTTPS вашей системы. Секрет подписи будет показан один раз."
        actions={
          <>
            <Button variant="ghost" onPress={() => setAdding(false)}>
              Отмена
            </Button>
            <Button
              variant="primary"
              isDisabled={!name.trim() || !url.trim() || create.isPending}
              onPress={() =>
                create.mutate(
                  { body: { name: name.trim(), url: url.trim() } },
                  {
                    onSuccess: (res) => {
                      setAdding(false);
                      setName("");
                      setUrl("");
                      setSecret(res.secret);
                      refresh();
                    },
                  },
                )
              }
            >
              Добавить
            </Button>
          </>
        }
      >
        <TextField label="Название" value={name} onChange={setName} maxLength={100} />
        <TextField label="Адрес" placeholder="https://example.com/freightflow" value={url} onChange={setUrl} />
        {create.error && (
          <div className="notice notice-error" role="alert">
            {problemMessage(create.error)}
          </div>
        )}
      </Modal>

      <Modal
        isOpen={secret !== null}
        onOpenChange={(open) => !open && setSecret(null)}
        title="Секрет подписи"
        description="Сохраните его в получающей системе: повторно он не показывается. Подпись — заголовок X-FreightFlow-Signature."
        actions={
          <Button variant="primary" onPress={() => setSecret(null)}>
            Готово
          </Button>
        }
      >
        <div className={s.secret}>
          <code>{secret}</code>
          <Button
            size="sm"
            variant="ghost"
            aria-label="Скопировать"
            onPress={() => {
              if (secret) {
                void navigator.clipboard.writeText(secret).then(() => toast.success("Скопировано"));
              }
            }}
          >
            <Copy size={15} aria-hidden />
          </Button>
        </div>
      </Modal>

      <Modal isOpen={log !== null} onOpenChange={(open) => !open && setLog(null)} title={`Журнал: ${log?.name ?? ""}`}>
        {log && <DeliveryLog id={log.id} />}
      </Modal>
    </Section>
  );
}

function DeliveryLog({ id }: { id: string }) {
  const list = $api.useQuery("get", "/me/webhooks/{webhookId}/deliveries", { params: { path: { webhookId: id } } });
  if (list.isPending) {
    return <Skeleton height={60} />;
  }
  if ((list.data ?? []).length === 0) {
    return <p className="muted">Доставок ещё не было.</p>;
  }
  return (
    <table className={tableClass} aria-label="Доставки вебхука">
      <thead>
        <tr>
          <th>Событие</th>
          <th>Статус</th>
          <th className={cellClass.right}>Ответ</th>
          <th>Когда</th>
        </tr>
      </thead>
      <tbody>
        {(list.data ?? []).map((d) => (
          <tr key={d.id}>
            <td>{d.event}</td>
            <td>
              <Badge tone={deliveryTone[d.status]}>{deliveryTitle[d.status]}</Badge>
              {d.lastError && <div className="muted">{d.lastError}</div>}
            </td>
            <td className={cellClass.right}>{d.responseCode ?? "—"}</td>
            <td>{dateTime(d.sentAt ?? d.createdAt)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
