import { useQueryClient } from "@tanstack/react-query";
import { Bookmark, Link2, Trash2 } from "lucide-react";
import { useState } from "react";
import { Dialog, DialogTrigger, Popover } from "react-aria-components";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { Button, Checkbox, IconButton, Modal, popoverClass, Select, TextField } from "@/shared/ui";
import { toast } from "@/shared/ui/toast";
import s from "./views.module.css";

type Page = Schemas["SavedView"]["page"];

const roleNames: Record<string, string> = {
  viewer: "наблюдатели",
  analyst: "аналитики",
  operator: "диспетчеры",
  data_steward: "редакторы данных",
  admin: "администраторы",
};

/**
 * Сохранённые виды страницы: применить, сохранить текущий (личный или общий для роли),
 * скопировать ссылку на текущий вид.
 */
export function ViewsMenu({
  page,
  params,
  onApply,
}: {
  page: Page;
  params: Record<string, unknown>;
  onApply: (params: Record<string, unknown>) => void;
}) {
  const qc = useQueryClient();
  const me = useMe();
  const views = $api.useQuery("get", "/views", { params: { query: { page } } });
  const refresh = () => qc.invalidateQueries({ queryKey: ["get", "/views"] });
  const remove = $api.useMutation("delete", "/views/{viewId}", {
    onSuccess: () => {
      toast.success("Вид удалён");
      void refresh();
    },
    onError: (e) => toast.error(problemMessage(e)),
  });
  const [saving, setSaving] = useState(false);
  const list = views.data ?? [];
  return (
    <>
      <DialogTrigger>
        <Button size="sm">
          <Bookmark size={14} aria-hidden /> Виды
        </Button>
        <Popover className={popoverClass} placement="bottom end">
          <Dialog className={s.menu} aria-label="Сохранённые виды">
            {({ close }) => (
              <>
                {list.length === 0 ? (
                  <p className={s.empty}>Сохранённых видов нет</p>
                ) : (
                  <ul className={s.list}>
                    {list.map((v) => (
                      <li key={v.id}>
                        <button
                          type="button"
                          className={s.apply}
                          onClick={() => {
                            onApply(v.params as Record<string, unknown>);
                            close();
                          }}
                        >
                          <span>{v.name}</span>
                          <span className={s.note}>
                            {v.role ? `общий: ${roleNames[v.role] ?? v.role}` : "личный"}
                            {!v.mine && ` · ${v.owner}`}
                          </span>
                        </button>
                        {v.mine && (
                          <IconButton
                            label={`Удалить вид «${v.name}»`}
                            onPress={() => remove.mutate({ params: { path: { viewId: v.id } } })}
                          >
                            <Trash2 size={14} />
                          </IconButton>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
                <div className={s.actions}>
                  {can(me.data, "views.manage") && (
                    <Button
                      size="sm"
                      variant="primary"
                      onPress={() => {
                        close();
                        setSaving(true);
                      }}
                    >
                      Сохранить текущий вид
                    </Button>
                  )}
                  <Button
                    size="sm"
                    variant="ghost"
                    onPress={() => {
                      void navigator.clipboard?.writeText(window.location.href);
                      toast.success("Ссылка на вид скопирована");
                      close();
                    }}
                  >
                    <Link2 size={14} aria-hidden /> Ссылка
                  </Button>
                </div>
              </>
            )}
          </Dialog>
        </Popover>
      </DialogTrigger>
      {saving && (
        <SaveDialog
          page={page}
          params={params}
          roles={me.data?.roles ?? []}
          onDone={() => {
            setSaving(false);
            void refresh();
          }}
        />
      )}
    </>
  );
}

function SaveDialog({
  page,
  params,
  roles,
  onDone,
}: {
  page: Page;
  params: Record<string, unknown>;
  roles: string[];
  onDone: () => void;
}) {
  const [name, setName] = useState("");
  const [shared, setShared] = useState(false);
  const [role, setRole] = useState(roles[0] ?? "");
  const create = $api.useMutation("post", "/views", {
    onSuccess: () => {
      toast.success("Вид сохранён");
      onDone();
    },
  });
  return (
    <Modal
      isOpen
      onOpenChange={(o) => !o && onDone()}
      title="Сохранить вид"
      description="Сохраняются слои, масштаб, центр и фильтры страницы."
      actions={
        <>
          <Button onPress={onDone}>Отмена</Button>
          <Button
            variant="primary"
            isDisabled={!name.trim() || create.isPending}
            onPress={() =>
              create.mutate({
                body: {
                  page,
                  name: name.trim(),
                  params: params as Record<string, never>,
                  role: shared && role ? role : undefined,
                },
              })
            }
          >
            Сохранить
          </Button>
        </>
      }
    >
      <div className={s.form}>
        <TextField
          label="Название"
          value={name}
          onChange={setName}
          errorMessage={create.error ? problemMessage(create.error) : undefined}
        />
        {roles.length > 0 && (
          <Checkbox isSelected={shared} onChange={setShared}>
            Общий для роли
          </Checkbox>
        )}
        {shared && roles.length > 1 && (
          <Select
            label="Роль"
            options={roles.map((r) => ({ id: r, label: roleNames[r] ?? r }))}
            value={role}
            onChange={setRole}
          />
        )}
      </div>
    </Modal>
  );
}
