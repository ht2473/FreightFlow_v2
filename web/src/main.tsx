import "./styles/app.css";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { router } from "./app/router";
import { isProblem } from "./shared/api/client";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      refetchOnWindowFocus: false,
      // Ошибки клиента (4xx) повторять бессмысленно.
      retry: (count, err) => !(isProblem(err) && err.status < 500) && count < 2,
    },
  },
});

const root = document.getElementById("root");
if (!root) {
  throw new Error("нет элемента #root");
}

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
