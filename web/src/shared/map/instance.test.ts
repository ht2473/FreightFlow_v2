import { describe, expect, it } from "vitest";
import { coverZoom, MIN_ZOOM } from "./instance";

describe("coverZoom", () => {
  it("на маленьком окне не опускается ниже наименьшего масштаба", () => {
    expect(coverZoom(300, 200)).toBe(MIN_ZOOM);
  });

  it("на большом окне рамка подложки закрывает окно целиком", () => {
    // Окно карты на мониторе 1920×1200: рамка 2,7° × 1,65° должна быть не меньше окна.
    const z = coverZoom(1230, 1030);
    const worldPx = 512 * 2 ** z;
    expect(((39.0 - 36.3) / 360) * worldPx).toBeGreaterThanOrEqual(1229);
    expect(z).toBeGreaterThan(MIN_ZOOM);
  });
});
