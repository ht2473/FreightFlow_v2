import { Lock } from "lucide-react";
import type { ReactNode } from "react";
import { can, type Permission, useMe } from "@/shared/auth";
import { EmptyState } from "@/shared/ui";

/** Показывает содержимое только при наличии права; иначе — понятное объяснение. */
export function Guard({ permission, children }: { permission: Permission; children: ReactNode }) {
  const me = useMe();
  if (!me.data) {
    return null;
  }
  if (!can(me.data, permission)) {
    return (
      <EmptyState icon={<Lock size={20} strokeWidth={1.7} />} title="Нет доступа">
        Раздел недоступен для вашей роли. Обратитесь к администратору.
      </EmptyState>
    );
  }
  return children;
}
