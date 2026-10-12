import { render, screen } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Badge, Button, EmptyState, Modal } from "./index";

describe("Button", () => {
  it("вызывает onPress и не реагирует в отключённом состоянии", async () => {
    const onPress = vi.fn();
    const { rerender } = render(<Button onPress={onPress}>Сохранить</Button>);
    await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(onPress).toHaveBeenCalledTimes(1);

    rerender(
      <Button onPress={onPress} isDisabled>
        Сохранить
      </Button>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(onPress).toHaveBeenCalledTimes(1);
  });

  it("собирает классы варианта", () => {
    render(
      <Button variant="primary" size="sm">
        Ок
      </Button>,
    );
    expect(screen.getByRole("button")).toHaveClass("btn", "btn-primary", "btn-sm");
  });
});

describe("Badge", () => {
  it("передаёт смысл словом, а не только цветом", () => {
    render(<Badge tone="crit">Заблокирован</Badge>);
    expect(screen.getByText("Заблокирован")).toHaveClass("badge-crit");
  });
});

describe("Modal", () => {
  it("показывает заголовок и закрывается по Escape", async () => {
    const onOpenChange = vi.fn();
    render(<Modal isOpen onOpenChange={onOpenChange} title="Отозвать токен" description="Действие необратимо" />);
    expect(screen.getByRole("dialog", { name: "Отозвать токен" })).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

describe("EmptyState", () => {
  it("выводит заголовок и пояснение", () => {
    render(<EmptyState title="Токенов нет">Выпустите токен</EmptyState>);
    expect(screen.getByText("Токенов нет")).toBeInTheDocument();
    expect(screen.getByText("Выпустите токен")).toBeInTheDocument();
  });
});
