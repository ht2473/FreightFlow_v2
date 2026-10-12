import { zodResolver } from "@hookform/resolvers/zod";
import { useQueryClient } from "@tanstack/react-query";
import { ShieldCheck, ShieldOff } from "lucide-react";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { $api } from "@/shared/api/client";
import { can, type Me, useMe } from "@/shared/auth";
import { roleTitle } from "@/shared/names";
import type { Theme } from "@/shared/theme";
import { useThemePreference } from "@/shared/themePreference";
import { Badge, Button, Modal, PageHeader, Section, Select } from "@/shared/ui";
import { applyServerErrors, FormTextField } from "@/shared/ui/form";
import { toast } from "@/shared/ui/toast";
import s from "./profile.module.css";
import { TokensSection } from "./TokensSection";

export function ProfilePage() {
  const me = useMe();
  if (!me.data) {
    return null;
  }
  return (
    <div className={s.page}>
      <PageHeader title="Профиль" subtitle={me.data.email} />
      <ProfileSection me={me.data} />
      <AppearanceSection />
      <PasswordSection />
      <TwoFactorSection me={me.data} />
      {can(me.data, "tokens.own") && <TokensSection me={me.data} />}
    </div>
  );
}

const profileSchema = z.object({
  name: z.string().trim().min(1, "Укажите имя").max(200, "Не длиннее 200 символов"),
});

function ProfileSection({ me }: { me: Me }) {
  const qc = useQueryClient();
  const form = useForm({ resolver: zodResolver(profileSchema), defaultValues: { name: me.name } });
  const update = $api.useMutation("patch", "/me", {
    onSuccess: (data) => {
      qc.setQueryData(["get", "/me", {}], data);
      form.reset({ name: data.name });
      toast.success("Имя сохранено");
    },
    onError: (e) => applyServerErrors(form.setError, e),
  });

  return (
    <Section title="Личные данные">
      <form className={s.form} onSubmit={form.handleSubmit((v) => update.mutate({ body: v }))} noValidate>
        <FormTextField control={form.control} name="name" label="Имя" autoComplete="name" />
        <div className="field">
          <span className="label">Роли</span>
          <div className={s.badges}>
            {me.roles.map((r) => (
              <Badge key={r} tone="info" plain>
                {roleTitle(r)}
              </Badge>
            ))}
          </div>
        </div>
        <div className={s.actions}>
          <Button type="submit" isDisabled={!form.formState.isDirty || update.isPending}>
            Сохранить
          </Button>
        </div>
      </form>
    </Section>
  );
}

const themeOptions = [
  { id: "system", label: "Как в системе" },
  { id: "light", label: "Светлая" },
  { id: "dark", label: "Тёмная" },
];

function AppearanceSection() {
  const { theme, change } = useThemePreference();
  return (
    <Section title="Оформление" description="Тема сохраняется в профиле и применяется на всех устройствах.">
      <div className={s.form}>
        <Select label="Тема" options={themeOptions} value={theme} onChange={(id) => change(id as Theme)} />
      </div>
    </Section>
  );
}

const passwordSchema = z
  .object({
    currentPassword: z.string().min(1, "Введите текущий пароль"),
    newPassword: z.string().min(12, "Не менее 12 символов").max(128, "Не длиннее 128 символов"),
    confirm: z.string(),
  })
  .refine((v) => v.newPassword === v.confirm, { path: ["confirm"], message: "Пароли не совпадают" });

function PasswordSection() {
  const form = useForm({
    resolver: zodResolver(passwordSchema),
    defaultValues: { currentPassword: "", newPassword: "", confirm: "" },
  });
  const change = $api.useMutation("post", "/me/password", {
    onSuccess: () => {
      form.reset();
      toast.success("Пароль изменён. Другие сеансы завершены.");
    },
    onError: (e) => applyServerErrors(form.setError, e, { password: "currentPassword" }),
  });

  return (
    <Section title="Пароль" description="После смены пароля другие сеансы завершаются.">
      <form
        className={s.form}
        onSubmit={form.handleSubmit((v) =>
          change.mutate({ body: { currentPassword: v.currentPassword, newPassword: v.newPassword } }),
        )}
        noValidate
      >
        <FormTextField
          control={form.control}
          name="currentPassword"
          label="Текущий пароль"
          type="password"
          autoComplete="current-password"
        />
        <FormTextField
          control={form.control}
          name="newPassword"
          label="Новый пароль"
          type="password"
          autoComplete="new-password"
          description="Не менее 12 символов"
        />
        <FormTextField
          control={form.control}
          name="confirm"
          label="Повторите новый пароль"
          type="password"
          autoComplete="new-password"
        />
        <div className={s.actions}>
          <Button type="submit" isDisabled={change.isPending}>
            Сменить пароль
          </Button>
        </div>
      </form>
    </Section>
  );
}

function TwoFactorSection({ me }: { me: Me }) {
  const [disabling, setDisabling] = useState(false);
  const mandatory = can(me, "users.manage");

  return (
    <Section
      title="Двухфакторная защита"
      description="Код из приложения-аутентификатора при входе. Резервные коды выдаются при подключении."
    >
      <div className={s.twoFactor}>
        {me.totpEnabled ? (
          <>
            <ShieldCheck size={20} strokeWidth={1.7} className={s.ok} aria-hidden />
            <div className={s.grow}>
              <Badge tone="ok">Включена</Badge>
              {mandatory && <p className="field-hint">Для администраторов защита обязательна.</p>}
            </div>
            {!mandatory && (
              <Button variant="ghost" onPress={() => setDisabling(true)}>
                Отключить
              </Button>
            )}
          </>
        ) : (
          <>
            <ShieldOff size={20} strokeWidth={1.7} className={s.muted} aria-hidden />
            <div className={s.grow}>
              <Badge tone="neutral">Не подключена</Badge>
            </div>
            <a className="btn btn-primary" href="/setup-2fa?next=/profile">
              Подключить
            </a>
          </>
        )}
      </div>
      <DisableTwoFactor isOpen={disabling} onClose={() => setDisabling(false)} />
    </Section>
  );
}

const confirmSchema = z.object({ password: z.string().min(1, "Введите пароль") });

function DisableTwoFactor({ isOpen, onClose }: { isOpen: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const form = useForm({ resolver: zodResolver(confirmSchema), defaultValues: { password: "" } });
  const disable = $api.useMutation("delete", "/me/totp", {
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["get", "/me"] });
      toast.success("Двухфакторная защита отключена");
      close();
    },
    onError: (e) => applyServerErrors(form.setError, e),
  });
  const close = () => {
    form.reset();
    onClose();
  };

  return (
    <Modal
      isOpen={isOpen}
      onOpenChange={(open) => !open && close()}
      title="Отключить двухфакторную защиту"
      description="Вход станет возможен только по паролю. Резервные коды будут удалены."
    >
      <form onSubmit={form.handleSubmit((v) => disable.mutate({ body: v }))} noValidate>
        <FormTextField
          control={form.control}
          name="password"
          label="Пароль"
          type="password"
          autoComplete="current-password"
          autoFocus
        />
        <div className={s.modalActions}>
          <Button variant="ghost" onPress={close}>
            Отмена
          </Button>
          <Button type="submit" variant="danger" isDisabled={disable.isPending}>
            Отключить защиту
          </Button>
        </div>
      </form>
    </Modal>
  );
}
