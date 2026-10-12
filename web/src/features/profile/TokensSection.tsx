import { zodResolver } from "@hookform/resolvers/zod";
import { useQueryClient } from "@tanstack/react-query";
import { addDays } from "date-fns";
import { Copy, KeyRound } from "lucide-react";
import { useState } from "react";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";
import { $api, type Schemas } from "@/shared/api/client";
import type { Me } from "@/shared/auth";
import { ago, date } from "@/shared/format";
import { Button, Checkbox, cellClass, EmptyState, Modal, Section, Select, Skeleton, tableClass } from "@/shared/ui";
import { applyServerErrors, FormTextField } from "@/shared/ui/form";
import { toast } from "@/shared/ui/toast";
import s from "./profile.module.css";

type ApiToken = Schemas["ApiToken"];

/** Токены API пользователя: выпуск, список, отзыв. */
export function TokensSection({ me }: { me: Me }) {
  const qc = useQueryClient();
  const tokens = $api.useQuery("get", "/me/tokens");
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<string | null>(null);
  const [revoking, setRevoking] = useState<ApiToken | null>(null);

  const revoke = $api.useMutation("delete", "/me/tokens/{tokenId}", {
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["get", "/me/tokens"] });
      toast.success("Токен отозван");
      setRevoking(null);
    },
    onError: () => toast.error("Не удалось отозвать токен"),
  });

  const active = (tokens.data ?? []).filter((t) => !t.revokedAt);

  return (
    <Section
      title="Токены API"
      description={
        <>
          Доступ к API без браузера: заголовок <code>Authorization: Bearer &lt;токен&gt;</code>.
        </>
      }
      actions={
        <Button size="sm" onPress={() => setCreating(true)}>
          Выпустить токен
        </Button>
      }
      flush
    >
      {tokens.isPending ? (
        <div className={s.pad}>
          <Skeleton height={32} />
        </div>
      ) : active.length === 0 ? (
        <EmptyState icon={<KeyRound size={20} strokeWidth={1.7} />} title="Токенов нет">
          Выпустите токен, чтобы обращаться к API из скриптов и внешних систем.
        </EmptyState>
      ) : (
        <table className={tableClass}>
          <thead>
            <tr>
              <th>Название</th>
              <th>Права</th>
              <th>Создан</th>
              <th>Действует до</th>
              <th>Использован</th>
              <th aria-label="Действия" />
            </tr>
          </thead>
          <tbody>
            {active.map((t) => (
              <tr key={t.id}>
                <td>
                  <div className={s.strong}>{t.name}</div>
                  <code className={s.muted}>{t.prefix}…</code>
                </td>
                <td>{t.scopes.length}</td>
                <td>{date(t.createdAt)}</td>
                <td>{t.expiresAt ? date(t.expiresAt) : "Бессрочно"}</td>
                <td>{t.lastUsedAt ? ago(t.lastUsedAt) : "—"}</td>
                <td className={cellClass.right}>
                  <Button size="sm" variant="ghost" onPress={() => setRevoking(t)}>
                    Отозвать
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <CreateToken
        me={me}
        isOpen={creating}
        onClose={() => setCreating(false)}
        onCreated={(value) => {
          setCreating(false);
          setCreated(value);
          void qc.invalidateQueries({ queryKey: ["get", "/me/tokens"] });
        }}
      />

      <Modal
        isOpen={created !== null}
        onOpenChange={(open) => !open && setCreated(null)}
        title="Токен выпущен"
        description="Скопируйте значение сейчас: повторно оно не показывается."
        actions={
          <Button variant="primary" onPress={() => setCreated(null)}>
            Готово
          </Button>
        }
      >
        <div className={s.secret}>
          <code>{created}</code>
          <Button
            size="sm"
            variant="ghost"
            aria-label="Скопировать"
            onPress={() => {
              if (created) {
                void navigator.clipboard.writeText(created).then(() => toast.success("Скопировано"));
              }
            }}
          >
            <Copy size={15} aria-hidden />
          </Button>
        </div>
      </Modal>

      <Modal
        isOpen={revoking !== null}
        onOpenChange={(open) => !open && setRevoking(null)}
        title="Отозвать токен"
        description={revoking ? `Токен «${revoking.name}» перестанет действовать сразу.` : undefined}
        actions={
          <>
            <Button variant="ghost" onPress={() => setRevoking(null)}>
              Отмена
            </Button>
            <Button
              variant="danger"
              isDisabled={revoke.isPending}
              onPress={() => revoking && revoke.mutate({ params: { path: { tokenId: revoking.id } } })}
            >
              Отозвать токен
            </Button>
          </>
        }
      />
    </Section>
  );
}

const lifetimes = [
  { id: "30", label: "30 дней" },
  { id: "90", label: "90 дней" },
  { id: "365", label: "1 год" },
  { id: "never", label: "Бессрочно" },
];

const tokenSchema = z.object({
  name: z.string().trim().min(1, "Укажите название").max(100, "Не длиннее 100 символов"),
  scopes: z.array(z.string()).min(1, "Выберите хотя бы одно право"),
  lifetime: z.string(),
});

function CreateToken({
  me,
  isOpen,
  onClose,
  onCreated,
}: {
  me: Me;
  isOpen: boolean;
  onClose: () => void;
  onCreated: (value: string) => void;
}) {
  const perms = $api.useQuery("get", "/permissions", {}, { enabled: isOpen, staleTime: Number.POSITIVE_INFINITY });
  const form = useForm({
    resolver: zodResolver(tokenSchema),
    defaultValues: { name: "", scopes: [] as string[], lifetime: "90" },
  });
  const create = $api.useMutation("post", "/me/tokens", {
    onSuccess: (data) => {
      form.reset();
      onCreated(data.value);
    },
    onError: (e) => applyServerErrors(form.setError, e),
  });
  const close = () => {
    form.reset();
    onClose();
  };

  // Токену можно передать только собственные права пользователя.
  const available = (perms.data ?? []).filter((p) => me.permissions.includes(p.code));

  return (
    <Modal isOpen={isOpen} onOpenChange={(open) => !open && close()} title="Новый токен API">
      <form
        onSubmit={form.handleSubmit((v) =>
          create.mutate({
            body: {
              name: v.name,
              scopes: v.scopes,
              expiresAt: v.lifetime === "never" ? undefined : addDays(new Date(), Number(v.lifetime)).toISOString(),
            },
          }),
        )}
        noValidate
      >
        <FormTextField control={form.control} name="name" label="Название" description="Например, «Выгрузка для BI»" />
        <Controller
          control={form.control}
          name="lifetime"
          render={({ field }) => (
            <div className="field">
              <Select label="Срок действия" options={lifetimes} value={field.value} onChange={field.onChange} />
            </div>
          )}
        />
        <Controller
          control={form.control}
          name="scopes"
          render={({ field, fieldState }) => (
            <fieldset className={`field ${s.fieldset}`}>
              <legend className="label">Права токена</legend>
              <div className={s.checks}>
                {available.map((p) => (
                  <Checkbox
                    key={p.code}
                    isSelected={field.value.includes(p.code)}
                    onChange={(on) =>
                      field.onChange(on ? [...field.value, p.code] : field.value.filter((c) => c !== p.code))
                    }
                  >
                    {p.title}
                  </Checkbox>
                ))}
              </div>
              {fieldState.error && <div className="field-error">{fieldState.error.message}</div>}
            </fieldset>
          )}
        />
        <div className={s.modalActions}>
          <Button variant="ghost" onPress={close}>
            Отмена
          </Button>
          <Button type="submit" variant="primary" isDisabled={create.isPending}>
            Выпустить
          </Button>
        </div>
      </form>
    </Modal>
  );
}
