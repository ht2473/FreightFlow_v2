import { writeFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";
import { account, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("выгрузки реестра во всех форматах: атрибуция и дата данных, GeoPackage", async ({ page }, info) => {
  test.setTimeout(240_000);
  await login(page, analyst, "/facilities");
  const filter = { type: ["logistics_center"] };
  for (const format of ["csv", "xlsx", "geojson", "gpkg"]) {
    const r = await page.request.post("/api/v1/exports", { data: { kind: "facilities", format, filter } });
    expect(r.status(), await r.text()).toBe(202);
    const { id } = (await r.json()) as { id: string };
    await expect
      .poll(
        async () => ((await (await page.request.get(`/api/v1/exports/${id}`)).json()) as { status: string }).status,
        {
          timeout: 180_000,
          intervals: [2_000],
        },
      )
      .toBe("done");
    const body = await (await page.request.get(`/api/v1/exports/${id}/file`)).body();
    await writeFile(info.outputPath(`objects.${format}`), body);
    if (format === "gpkg") {
      expect(body.subarray(0, 15).toString()).toBe("SQLite format 3");
    } else if (format === "xlsx") {
      expect(body.subarray(0, 2).toString()).toBe("PK");
    } else {
      expect(body.toString()).toMatch(/данные реестра на \d\d\.\d\d\.\d{4}/);
    }
  }
});
