import { useParams, useSearch } from "@tanstack/react-router";
import { useLayoutEffect } from "react";
import {
  FacilityReport,
  PassabilityReport,
  QualityReport,
  RoadworksReport,
  ScenarioReport,
  SiteReport,
  TerritoryReport,
} from "./templates";

const templates = {
  facility: FacilityReport,
  territory: TerritoryReport,
  passability: PassabilityReport,
  roadworks: RoadworksReport,
  scenario: ScenarioReport,
  site: SiteReport,
  quality: QualityReport,
} as const;

/** Страница печати отчёта /print/<шаблон>: её открывает служба pdf и пользователь для предпросмотра. */
export function PrintPage() {
  const { template } = useParams({ from: "/print/$template" });
  const params = useSearch({ from: "/print/$template" });
  const Template = templates[template as keyof typeof templates];
  useLayoutEffect(() => {
    if (!Template) {
      document.body.dataset.error = "Неизвестный шаблон отчёта";
      document.body.dataset.ready = "error";
    }
  }, [Template]);
  if (!Template) {
    return <p role="alert">Неизвестный шаблон отчёта.</p>;
  }
  return <Template params={params} />;
}
