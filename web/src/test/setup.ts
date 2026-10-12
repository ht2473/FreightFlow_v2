import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Без глобальных функций Vitest очистка DOM между тестами не подключается сама.
afterEach(() => {
  cleanup();
});
