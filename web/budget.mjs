// Бюджет стартовой загрузки фронтенда: скрипты из dist/index.html (сжатые gzip) не тяжелее
// предела, а карта и графики подгружаются только на своих страницах (03-architecture §24).
import { readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";

const limitKb = 320;
const lazy = ["map-", "charts-"];

const html = readFileSync(new URL("./dist/index.html", import.meta.url), "utf8");
const scripts = [...html.matchAll(/assets\/[^"]+\.js/g)].map((m) => m[0]);
let total = 0;
const errors = [];
for (const s of scripts) {
  const kb = gzipSync(readFileSync(new URL(`./dist/${s}`, import.meta.url))).length / 1024;
  total += kb;
  if (lazy.some((p) => s.startsWith(`assets/${p}`))) {
    errors.push(`${s} грузится на старте, а должен — по требованию`);
  }
}
console.log(`Стартовая загрузка: ${scripts.length} скриптов, ${total.toFixed(0)} КБ gzip (предел ${limitKb})`);
if (total > limitKb) {
  errors.push(`стартовая загрузка ${total.toFixed(0)} КБ больше ${limitKb} КБ`);
}
if (errors.length > 0) {
  console.error(errors.join("\n"));
  process.exit(1);
}
