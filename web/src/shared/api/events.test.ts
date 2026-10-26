import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useServerEvent } from "./events";

class FakeEventSource {
  static all: FakeEventSource[] = [];
  url: string;
  closed = false;
  private handlers = new Map<string, ((e: unknown) => void)[]>();
  constructor(url: string) {
    this.url = url;
    FakeEventSource.all.push(this);
  }
  addEventListener(type: string, h: (e: unknown) => void) {
    this.handlers.set(type, [...(this.handlers.get(type) ?? []), h]);
  }
  close() {
    this.closed = true;
  }
  fire(type: string, data?: string) {
    for (const h of this.handlers.get(type) ?? []) {
      h({ data });
    }
  }
}

beforeEach(() => {
  FakeEventSource.all = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useServerEvent", () => {
  it("одно соединение на всех подписчиков, закрывается с последним", () => {
    const a = vi.fn();
    const b = vi.fn();
    const ha = renderHook(() => useServerEvent("scenario.computed", a));
    const hb = renderHook(() => useServerEvent("job.progress", b));
    expect(FakeEventSource.all).toHaveLength(1);
    const es = FakeEventSource.all[0];
    expect(es?.url).toBe("/api/v1/events");

    es?.fire("scenario.computed", '{"id":"s1"}');
    es?.fire("job.progress", "не json");
    expect(a).toHaveBeenCalledWith({ id: "s1" });
    expect(b).not.toHaveBeenCalled();

    ha.unmount();
    expect(es?.closed).toBe(false);
    hb.unmount();
    expect(es?.closed).toBe(true);
  });

  it("сообщает о восстановлении соединения после разрыва", () => {
    const re = vi.fn();
    const h = renderHook(() => useServerEvent("reconnected", re));
    const es = FakeEventSource.all[0];
    es?.fire("open");
    expect(re).not.toHaveBeenCalled();
    es?.fire("error");
    es?.fire("open");
    expect(re).toHaveBeenCalledTimes(1);
    h.unmount();
  });
});
