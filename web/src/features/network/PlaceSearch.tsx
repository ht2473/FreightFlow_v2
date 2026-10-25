import { useState } from "react";
import { $api } from "@/shared/api/client";
import type { Hit } from "@/shared/search";
import { SuggestField } from "@/shared/ui/fields";
import { useDebounced } from "@/shared/useDebounced";

/** Место маршрута: найденный документ с точкой. */
export type Place = Hit & { point: NonNullable<Hit["point"]> };

const placeKey = (p: Hit) => `${p.kind}:${p.ref}`;

const kindNote: Partial<Record<Hit["kind"], string>> = { facility: "Объект реестра", street: "Улица" };

/** Поиск точки по адресу или улице (адресный реестр Москвы) и названию объекта реестра. */
export function PlaceSearch({ label, onSelect }: { label: string; onSelect: (p: Place) => void }) {
  const [text, setText] = useState("");
  const q = useDebounced(text.trim(), 200);
  const enabled = q.length >= 2;
  const places = $api.useQuery(
    "get",
    "/search/suggest",
    { params: { query: { q, kind: ["facility", "address", "street"] } } },
    { enabled, staleTime: 60_000 },
  );
  const list = enabled ? (places.data ?? []).filter((p): p is Place => p.point !== undefined) : [];
  return (
    <SuggestField
      label={label}
      placeholder="Улица и дом или название объекта"
      text={text}
      onTextChange={setText}
      pending={enabled && (places.isFetching || q !== text.trim())}
      suggestions={list.map((p) => ({
        id: placeKey(p),
        label: p.title,
        description: [kindNote[p.kind], p.subtitle].filter(Boolean).join(" · "),
      }))}
      onSelect={(id) => {
        const p = list.find((x) => placeKey(x) === id);
        if (p) {
          onSelect(p);
        }
      }}
    />
  );
}
