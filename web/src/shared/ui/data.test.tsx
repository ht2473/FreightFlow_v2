import { render, screen } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { num } from "@/shared/format";
import { KpiTile } from "./KpiTile";
import { Pagination } from "./navigation";

describe("num", () => {
  it("форматирует по-русски", () => {
    expect(num(48000)).toBe("48 000");
    expect(num(3.456, 2)).toBe("3,46");
    expect(num(null)).toBe("—");
  });
});

describe("KpiTile", () => {
  it("называет ухудшение словом, а не только цветом", () => {
    render(
      <KpiTile
        label="Работы на сети"
        value={37}
        delta={{ value: 5, period: "за неделю", better: "down" }}
        source="портал"
      />,
    );
    expect(screen.getByText("(ухудшение)")).toBeInTheDocument();
    expect(screen.getByText("Источник: портал")).toBeInTheDocument();
  });

  it("показывает прочерк без данных и метку модели", () => {
    render(<KpiTile label="Время" value={null} unit="мин" model source="расчёт" />);
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.queryByText("мин")).not.toBeInTheDocument();
    expect(screen.getByText("модель")).toBeInTheDocument();
  });
});

describe("Pagination", () => {
  it("показывает диапазон и блокирует переход за границы", async () => {
    const onChange = vi.fn();
    render(<Pagination offset={36} limit={12} total={48} onChange={onChange} />);
    expect(screen.getByText("37–48 из 48")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Следующая страница" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Предыдущая страница" }));
    expect(onChange).toHaveBeenCalledWith(24);
  });
});
