import { Check } from "lucide-react";
import { Guard } from "@/app/Guard";
import { $api } from "@/shared/api/client";
import { cellClass, PageHeader, Section, Skeleton, tableClass } from "@/shared/ui";
import s from "./admin.module.css";

export function RolesPage() {
  return (
    <Guard permission="users.manage">
      <RoleMatrix />
    </Guard>
  );
}

/** Матрица «право × роль» встроенных ролей. */
function RoleMatrix() {
  const roles = $api.useQuery("get", "/admin/roles");
  const perms = $api.useQuery("get", "/permissions", {}, { staleTime: Number.POSITIVE_INFINITY });

  return (
    <>
      <PageHeader title="Роли и права" subtitle="Встроенные роли и состав их прав" />
      <div className={s.roleCards}>
        {(roles.data ?? []).map((r) => (
          <div key={r.code} className={`card ${s.roleCard}`}>
            <div className={s.strong}>{r.name}</div>
            <div className={s.muted}>{r.description}</div>
            <div className={s.roleCount}>{r.permissions.length} прав</div>
          </div>
        ))}
      </div>
      <Section flush>
        {roles.isPending || perms.isPending ? (
          <div className={s.pad}>
            <Skeleton height={36} />
          </div>
        ) : (
          <div className={s.scrollX}>
            <table className={tableClass}>
              <thead>
                <tr>
                  <th>Право</th>
                  {(roles.data ?? []).map((r) => (
                    <th key={r.code} className={cellClass.center}>
                      {r.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {(perms.data ?? []).map((p) => (
                  <tr key={p.code}>
                    <td>
                      <div>{p.title}</div>
                      <code className={s.muted}>{p.code}</code>
                    </td>
                    {(roles.data ?? []).map((r) => (
                      <td key={r.code} className={cellClass.center}>
                        {r.permissions.includes(p.code) ? (
                          <Check size={16} className={s.yes} role="img" aria-label="Есть" />
                        ) : (
                          <span className={s.no}>
                            <span aria-hidden>—</span>
                            <span className="visually-hidden">Нет</span>
                          </span>
                        )}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Section>
    </>
  );
}
