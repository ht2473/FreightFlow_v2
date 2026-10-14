import { useState } from "react";
import { $api, type Schemas } from "@/shared/api/client";
import { SuggestField } from "@/shared/ui/fields";
import { useDebounced } from "@/shared/useDebounced";

type Place = Schemas["Place"];

const placeKey = (p: Place) => `${p.kind}:${p.id}`;

/** Поиск точки по адресу (адресный реестр Москвы) или названию объекта реестра. */
export function PlaceSearch({ label, onSelect }: { label: string; onSelect: (p: Place) => void }) {
  const [text, setText] = useState("");
  const q = useDebounced(text.trim(), 250);
  const enabled = q.length >= 3;
  const places = $api.useQuery("get", "/routing/places", { params: { query: { q } } }, { enabled, staleTime: 60_000 });
  const list = enabled ? (places.data ?? []) : [];
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
        description: p.kind === "facility" ? `Объект реестра · ${p.subtitle}` : p.subtitle,
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
