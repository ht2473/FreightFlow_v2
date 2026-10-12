import { zodResolver } from "@hookform/resolvers/zod";
import { useQueryClient } from "@tanstack/react-query";
import { MoreHorizontal, Search, Users } from "lucide-react";
import { useMemo, useState } from "react";
import { Input, Menu, MenuItem, MenuTrigger, Popover, SearchField, Separator } from "react-aria-components";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { useMe } from "@/shared/auth";
import { ago } from "@/shared/format";
import { roleTitle } from "@/shared/names";
import {
  Badge,
  Button,
  Checkbox,
  cellClass,
  EmptyState,
  IconButton,
  Modal,
  menuItemClass,
  menuSeparatorClass,
  PageHeader,
  popoverClass,
  Section,
  Select,
  Skeleton,
  tableClass,
} from "@/shared/ui";
import { applyServerErrors, FormTextField } from "@/shared/ui/form";
import { toast } from "@/shared/ui/toast";
import s from "./admin.module.css";

type User = Schemas["User"];
type Dialog = { kind: "create" } | { kind: "invite" } | { kind: "edit"; user: User } | { kind: "reset2fa"; user: User };

export function UsersPage() {
  return (
    <Guard permission="users.manage">
      <UserList />
    </Guard>
  );
}

function UserList() {
  const qc = useQueryClient();
  const me = useMe();
  const users = $api.useQuery("get", "/admin/users");
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const close = () => setDialog(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["get", "/admin/users"] });

  const setStatus = $api.useMutation("patch", "/admin/users/{userId}", {
    onSuccess: (u) => {
      void refresh();
      toast.success(u.status === "blocked" ? "Пользователь заблокирован" : "Пользователь разблокирован");
    },
    onError: (e) => toast.error(problemMessage(e)),
  });

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = users.data ?? [];
    return q ? list.filter((u) => u.name.toLowerCase().includes(q) || u.email.includes(q)) : list;
  }, [users.data, query]);

  const onAction = (u: User, key: string) => {
    switch (key) {
      case "edit":
        setDialog({ kind: "edit", user: u });
        break;
      case "reset2fa":
        setDialog({ kind: "reset2fa", user: u });
        break;
      case "block":
      case "unblock":
        setStatus.mutate({
          params: { path: { userId: u.id } },
          body: { status: key === "block" ? "blocked" : "active" },
        });
        break;
    }
  };

  return (
    <>
      <PageHeader
        title="Пользователи"
        subtitle="Учётные записи, роли и доступ"
        actions={
          <>
            <Button onPress={() => setDialog({ kind: "create" })}>Создать</Button>
            <Button variant="primary" onPress={() => setDialog({ kind: "invite" })}>
              Пригласить
            </Button>
          </>
        }
      />
      <Section flush>
        <div className={s.toolbar}>
          <SearchField aria-label="Поиск пользователей" value={query} onChange={setQuery} className={s.search}>
            <Search size={15} aria-hidden />
            <Input placeholder="Имя или адрес" />
          </SearchField>
          {users.data && (
            <span className={s.count}>
              {filtered.length} из {users.data.length}
            </span>
          )}
        </div>
        {users.isPending ? (
          <div className={s.pad}>
            <Skeleton height={36} />
          </div>
        ) : filtered.length === 0 ? (
          <EmptyState icon={<Users size={20} strokeWidth={1.7} />} title="Никого не найдено">
            Измените запрос поиска.
          </EmptyState>
        ) : (
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Пользователь</th>
                <th>Роли</th>
                <th>2FA</th>
                <th>Статус</th>
                <th>Последний вход</th>
                <th aria-label="Действия" />
              </tr>
            </thead>
            <tbody>
              {filtered.map((u) => (
                <tr key={u.id}>
                  <td>
                    <div className={s.strong}>
                      {u.name}
                      {u.id === me.data?.id && <span className={s.muted}> (вы)</span>}
                    </div>
                    <div className={s.muted}>{u.email}</div>
                  </td>
                  <td>
                    <div className={s.badges}>
                      {u.roles.map((r) => (
                        <Badge key={r} tone="info" plain>
                          {roleTitle(r)}
                        </Badge>
                      ))}
                    </div>
                  </td>
                  <td>{u.totpEnabled ? <Badge tone="ok">Включена</Badge> : <Badge>Нет</Badge>}</td>
                  <td>
                    {u.status === "active" ? <Badge tone="ok">Активен</Badge> : <Badge tone="crit">Заблокирован</Badge>}
                  </td>
                  <td className={s.muted}>{u.lastLoginAt ? ago(u.lastLoginAt) : "Не входил"}</td>
                  <td className={cellClass.right}>
                    <MenuTrigger>
                      <IconButton label={`Действия: ${u.name}`}>
                        <MoreHorizontal size={17} />
                      </IconButton>
                      <Popover className={popoverClass} placement="bottom end">
                        <Menu aria-label="Действия" onAction={(k) => onAction(u, String(k))}>
                          <MenuItem id="edit" className={menuItemClass}>
                            Изменить
                          </MenuItem>
                          {u.totpEnabled && (
                            <MenuItem id="reset2fa" className={menuItemClass}>
                              Сбросить 2FA
                            </MenuItem>
                          )}
                          {u.id !== me.data?.id && (
                            <>
                              <Separator className={menuSeparatorClass} />
                              {u.status === "active" ? (
                                <MenuItem id="block" className={menuItemClass}>
                                  Заблокировать
                                </MenuItem>
                              ) : (
                                <MenuItem id="unblock" className={menuItemClass}>
                                  Разблокировать
                                </MenuItem>
                              )}
                            </>
                          )}
                        </Menu>
                      </Popover>
                    </MenuTrigger>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Section>

      <CreateUserDialog isOpen={dialog?.kind === "create"} onClose={close} onDone={refresh} />
      <InviteDialog isOpen={dialog?.kind === "invite"} onClose={close} />
      {dialog?.kind === "edit" && <EditUserDialog user={dialog.user} onClose={close} onDone={refresh} />}
      {dialog?.kind === "reset2fa" && <ResetTwoFactorDialog user={dialog.user} onClose={close} onDone={refresh} />}
    </>
  );
}

/** Флажки ролей для форм. */
function RolePicker({ value, onChange, error }: { value: string[]; onChange: (v: string[]) => void; error?: string }) {
  const roles = $api.useQuery("get", "/admin/roles", {}, { staleTime: Number.POSITIVE_INFINITY });
  return (
    <fieldset className={`field ${s.fieldset}`}>
      <legend className="label">Роли</legend>
      <div className={s.roleList}>
        {(roles.data ?? []).map((r) => (
          <Checkbox
            key={r.code}
            isSelected={value.includes(r.code)}
            onChange={(on) => onChange(on ? [...value, r.code] : value.filter((c) => c !== r.code))}
          >
            <span>
              <span className={s.strong}>{r.name}</span>
              <span className={s.roleHint}>{r.description}</span>
            </span>
          </Checkbox>
        ))}
      </div>
      {error && <div className="field-error">{error}</div>}
    </fieldset>
  );
}

const rolesField = z.array(z.string()).min(1, "Выберите хотя бы одну роль");

const createSchema = z.object({
  email: z.email("Некорректный адрес"),
  name: z.string().trim().min(1, "Укажите имя").max(200),
  password: z.string().min(12, "Не менее 12 символов").max(128),
  roles: rolesField,
});

function CreateUserDialog({ isOpen, onClose, onDone }: { isOpen: boolean; onClose: () => void; onDone: () => void }) {
  const form = useForm({
    resolver: zodResolver(createSchema),
    defaultValues: { email: "", name: "", password: "", roles: ["viewer"] },
  });
  const create = $api.useMutation("post", "/admin/users", {
    onSuccess: () => {
      toast.success("Пользователь создан");
      onDone();
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
      title="Новый пользователь"
      description="Передайте пароль пользователю лично; при первом входе его стоит сменить."
    >
      <form onSubmit={form.handleSubmit((v) => create.mutate({ body: v }))} noValidate>
        <FormTextField control={form.control} name="email" label="Электронная почта" type="email" autoFocus />
        <FormTextField control={form.control} name="name" label="Имя" />
        <FormTextField
          control={form.control}
          name="password"
          label="Пароль"
          type="password"
          autoComplete="new-password"
          description="Не менее 12 символов"
        />
        <Controller
          control={form.control}
          name="roles"
          render={({ field, fieldState }) => (
            <RolePicker value={field.value} onChange={field.onChange} error={fieldState.error?.message} />
          )}
        />
        <div className={s.modalActions}>
          <Button variant="ghost" onPress={close}>
            Отмена
          </Button>
          <Button type="submit" variant="primary" isDisabled={create.isPending}>
            Создать
          </Button>
        </div>
      </form>
    </Modal>
  );
}

const inviteSchema = z.object({
  email: z.email("Некорректный адрес"),
  role: z.string().min(1),
});

function InviteDialog({ isOpen, onClose }: { isOpen: boolean; onClose: () => void }) {
  const roles = $api.useQuery("get", "/admin/roles", {}, { enabled: isOpen, staleTime: Number.POSITIVE_INFINITY });
  const form = useForm({ resolver: zodResolver(inviteSchema), defaultValues: { email: "", role: "viewer" } });
  const invite = $api.useMutation("post", "/admin/invites", {
    onSuccess: (r) => {
      toast.success(`Приглашение отправлено на ${r.email}`);
      close();
    },
    onError: (e) => applyServerErrors(form.setError, e),
  });
  const close = () => {
    form.reset();
    onClose();
  };
  const options = (roles.data ?? []).map((r) => ({ id: r.code, label: r.name }));

  return (
    <Modal
      isOpen={isOpen}
      onOpenChange={(open) => !open && close()}
      title="Пригласить пользователя"
      description="Ссылка для создания учётной записи придёт на почту и действует 72 часа."
    >
      <form onSubmit={form.handleSubmit((v) => invite.mutate({ body: v }))} noValidate>
        <FormTextField control={form.control} name="email" label="Электронная почта" type="email" autoFocus />
        <Controller
          control={form.control}
          name="role"
          render={({ field }) => (
            <div className="field">
              <Select label="Роль" options={options} value={field.value} onChange={field.onChange} />
            </div>
          )}
        />
        <div className={s.modalActions}>
          <Button variant="ghost" onPress={close}>
            Отмена
          </Button>
          <Button type="submit" variant="primary" isDisabled={invite.isPending}>
            Отправить приглашение
          </Button>
        </div>
      </form>
    </Modal>
  );
}

const editSchema = z.object({
  name: z.string().trim().min(1, "Укажите имя").max(200),
  roles: rolesField,
});

function EditUserDialog({ user, onClose, onDone }: { user: User; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const me = useMe();
  const form = useForm({ resolver: zodResolver(editSchema), defaultValues: { name: user.name, roles: user.roles } });
  const update = $api.useMutation("patch", "/admin/users/{userId}", {
    onSuccess: () => {
      toast.success("Изменения сохранены");
      onDone();
      if (user.id === me.data?.id) {
        void qc.invalidateQueries({ queryKey: ["get", "/me"] });
      }
      onClose();
    },
    onError: (e) => applyServerErrors(form.setError, e),
  });

  return (
    <Modal isOpen onOpenChange={(open) => !open && onClose()} title="Изменить пользователя" description={user.email}>
      <form
        onSubmit={form.handleSubmit((v) => update.mutate({ params: { path: { userId: user.id } }, body: v }))}
        noValidate
      >
        <FormTextField control={form.control} name="name" label="Имя" />
        <Controller
          control={form.control}
          name="roles"
          render={({ field, fieldState }) => (
            <RolePicker value={field.value} onChange={field.onChange} error={fieldState.error?.message} />
          )}
        />
        <div className={s.modalActions}>
          <Button variant="ghost" onPress={onClose}>
            Отмена
          </Button>
          <Button type="submit" variant="primary" isDisabled={update.isPending || !form.formState.isDirty}>
            Сохранить
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function ResetTwoFactorDialog({ user, onClose, onDone }: { user: User; onClose: () => void; onDone: () => void }) {
  const reset = $api.useMutation("delete", "/admin/users/{userId}/totp", {
    onSuccess: () => {
      toast.success("Двухфакторная защита сброшена");
      onDone();
      onClose();
    },
    onError: (e) => toast.error(problemMessage(e)),
  });
  return (
    <Modal
      isOpen
      onOpenChange={(open) => !open && onClose()}
      title="Сбросить двухфакторную защиту"
      description={`${user.name} войдёт по паролю и подключит защиту заново. Резервные коды будут удалены.`}
      actions={
        <>
          <Button variant="ghost" onPress={onClose}>
            Отмена
          </Button>
          <Button
            variant="danger"
            isDisabled={reset.isPending}
            onPress={() => reset.mutate({ params: { path: { userId: user.id } } })}
          >
            Сбросить 2FA
          </Button>
        </>
      }
    />
  );
}
