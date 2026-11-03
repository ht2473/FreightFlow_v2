import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ListChecks, Pencil, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { $api, problemMessage } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { ago, plural } from "@/shared/format";
import {
  Badge,
  Button,
  cellClass,
  EmptyState,
  IconButton,
  Modal,
  Section,
  Skeleton,
  Switch,
  tableClass,
} from "@/shared/ui";
import { toast } from "@/shared/ui/toast";
import s from "./alerts.module.css";
import { type AlertRule, channelTitle, kindTitle, scopeSummary, severityTitle, severityTone } from "./labels";

export function RulesList() {
  const me = useMe();
  const manage = can(me.data, "alerts.manage");
  const [all, setAll] = useState(false);
  const list = $api.useQuery("get", "/alert-rules", { params: { query: { all: all || undefined } } });
  return (
    <Section
      title={all ? "Правила всех пользователей" : "Мои правила"}
      flush
      actions={
        manage ? (
          <Switch isSelected={all} onChange={setAll}>
            Все правила
          </Switch>
        ) : undefined
      }
    >
      {list.isPending ? (
        <Skeleton height={80} />
      ) : list.error ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(list.error)}
        </div>
      ) : (list.data ?? []).length === 0 ? (
        <EmptyState
          icon={<ListChecks size={20} strokeWidth={1.7} />}
          title="Правил пока нет"
          action={
            <Link to="/alerts/rules/new" className="btn btn-sm btn-primary">
              Создать правило
            </Link>
          }
        >
          <p>Правило следит за работами на маршруте, ограничениями, доступностью объектов или источниками.</p>
        </EmptyState>
      ) : (
        <table className={tableClass} aria-label="Правила оповещений">
          <thead>
            <tr>
              <th>Правило</th>
              <th>Вид и область</th>
              <th>Важность</th>
              <th>Каналы</th>
              <th className={cellClass.right}>Открытых алертов</th>
              <th>Проверено</th>
              <th>Включено</th>
              <th className={cellClass.right}>
                <span className="visually-hidden">Действия</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {(list.data ?? []).map((r) => (
              <RuleRow key={r.id} rule={r} own={r.ownerId === me.data?.id} />
            ))}
          </tbody>
        </table>
      )}
    </Section>
  );
}

function RuleRow({ rule: r, own }: { rule: AlertRule; own: boolean }) {
  const qc = useQueryClient();
  const refresh = () =>
    void qc.invalidateQueries({
      predicate: (q) => typeof q.queryKey[1] === "string" && /^\/(alert-rules|alerts)/.test(q.queryKey[1]),
    });
  const update = $api.useMutation("put", "/alert-rules/{ruleId}", { onSuccess: refresh });
  const check = $api.useMutation("post", "/alert-rules/{ruleId}/check", {
    onSuccess: (res) => {
      refresh();
      toast.success(
        res.hits > 0
          ? `Проверено: ${res.hits} ${plural(res.hits, "новое срабатывание", "новых срабатывания", "новых срабатываний")}`
          : "Проверено: новых срабатываний нет",
      );
    },
  });
  const remove = $api.useMutation("delete", "/alert-rules/{ruleId}", { onSuccess: refresh });
  const [confirm, setConfirm] = useState(false);
  const path = { params: { path: { ruleId: r.id } } };
  const error = update.error ?? check.error ?? remove.error;
  return (
    <tr>
      <td>
        <div>{r.name}</div>
        {!own && <div className="muted">{r.ownerName}</div>}
        {r.checkError && <div className={s.error}>Проверка не удалась: {r.checkError}</div>}
        {error && <div className={s.error}>{problemMessage(error)}</div>}
      </td>
      <td>
        <div>{kindTitle[r.kind]}</div>
        <div className="muted">{scopeSummary(r)}</div>
      </td>
      <td>
        <Badge tone={severityTone[r.severity]}>{severityTitle[r.severity]}</Badge>
      </td>
      <td>
        <span className={s.channels}>
          {r.channels.map((c) => (
            <Badge key={c} plain>
              {channelTitle[c]}
            </Badge>
          ))}
        </span>
      </td>
      <td className={cellClass.right}>{r.openAlerts}</td>
      <td>{r.checkedAt ? ago(r.checkedAt) : "ещё нет"}</td>
      <td>
        <Switch
          aria-label={`Правило «${r.name}» включено`}
          isSelected={r.enabled}
          isDisabled={!own || update.isPending}
          onChange={(enabled) =>
            update.mutate({
              ...path,
              body: {
                name: r.name,
                kind: r.kind,
                scope: r.scope,
                params: r.params,
                severity: r.severity,
                channels: r.channels,
                enabled,
              },
            })
          }
        >
          <span className="visually-hidden">Включено</span>
        </Switch>
      </td>
      <td className={cellClass.right}>
        {own && (
          <span className={s.inline}>
            <IconButton label={`Проверить «${r.name}» сейчас`} onPress={() => check.mutate(path)}>
              <RefreshCw size={15} />
            </IconButton>
            <Link
              to="/alerts/rules/$ruleId"
              params={{ ruleId: r.id }}
              className="btn btn-sm btn-ghost"
              aria-label={`Изменить «${r.name}»`}
            >
              <Pencil size={15} aria-hidden />
            </Link>
            <IconButton label={`Удалить «${r.name}»`} onPress={() => setConfirm(true)}>
              <Trash2 size={15} />
            </IconButton>
          </span>
        )}
        <Modal
          isOpen={confirm}
          onOpenChange={setConfirm}
          title="Удалить правило?"
          description={`Правило «${r.name}» и его алерты будут удалены.`}
          actions={
            <>
              <Button onPress={() => setConfirm(false)}>Отмена</Button>
              <Button
                variant="danger"
                onPress={() => remove.mutate(path, { onSuccess: () => setConfirm(false) })}
                isDisabled={remove.isPending}
              >
                Удалить
              </Button>
            </>
          }
        />
      </td>
    </tr>
  );
}
